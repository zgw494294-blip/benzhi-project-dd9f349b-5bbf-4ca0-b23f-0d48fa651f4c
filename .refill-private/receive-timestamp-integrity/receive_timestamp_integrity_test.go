package receivetimestampintegrity

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func makeDispatchedBatch(t *testing.T, now time.Time) domain.DeliveryBatch {
	t.Helper()
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Dispatch(&batch, map[string]string{"box": "seal"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.AppendHandoff(&batch, domain.HandoffInput{FromParty: "A", ToParty: "B", Location: "中转点", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "handoff-1"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestReceivePreservesMonotonicTimelineTimestamps(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	first := makeDispatchedBatch(t, now)
	if err := domain.ReceiveBox(&first, domain.ReceiveInput{BoxID: "box", Quantity: 1, Condition: domain.ConditionAccepted}, now.Add(90*time.Second)); !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("早于交接时间的签收应被拒绝: %v", err)
	}
	second := makeDispatchedBatch(t, now)
	second.UpdatedAt = now.Add(5 * time.Minute)
	if err := domain.ReceiveBox(&second, domain.ReceiveInput{BoxID: "box", Quantity: 1, Condition: domain.ConditionAccepted}, now.Add(3*time.Minute)); !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("倒退批次更新时间的签收应被拒绝: %v", err)
	}
}
