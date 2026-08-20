package handoffreplay

import (
	"context"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/repository"
	"coldchain-route-ledger/internal/service"
)

func TestHandoffReplayNormalizesEquivalentPayload(t *testing.T) {
	clock := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	svc := service.NewWithClock(repository.NewMemory(), func() time.Time { return clock })
	ctx := context.Background()
	created, err := svc.CreateBatch(ctx, service.CreateBatchRequest{RouteDate: "2026-08-19", Origin: "社区药房", Destination: "卫生站", Boxes: []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}})
	if err != nil {
		t.Fatal(err)
	}
	dispatched, err := svc.Dispatch(ctx, created.ID, service.DispatchRequest{ExpectedVersion: 1, Seals: map[string]string{"box": "seal"}})
	if err != nil {
		t.Fatal(err)
	}
	request := service.HandoffRequest{ExpectedVersion: dispatched.Version, FromParty: "社区药房", ToParty: "配送员", Location: "冷库", TemperatureCelsius: 5, Unit: "C", Notes: "交接", IdempotencyKey: "same-key"}
	first, err := svc.Handoff(ctx, created.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.FromParty = " 社区药房 "
	request.ToParty = " 配送员 "
	request.Location = " 冷库 "
	request.Notes = " 交接 "
	replay, err := svc.Handoff(ctx, created.ID, request)
	if err != nil || !replay.Replayed || replay.Event.ID != first.Event.ID {
		t.Fatalf("等价载荷应重放原交接: replay=%+v err=%v", replay, err)
	}
}
