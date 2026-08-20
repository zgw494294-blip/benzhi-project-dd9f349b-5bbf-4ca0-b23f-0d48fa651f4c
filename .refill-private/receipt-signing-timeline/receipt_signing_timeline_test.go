package receiptsigningtimeline

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/ledger"
)

func TestBuildReceiptRejectsSigningBeforeTimeline(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{{
		ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8,
	}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Dispatch(&batch, map[string]string{"box": "seal"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.AppendHandoff(&batch, domain.HandoffInput{
		FromParty: "A", ToParty: "B", Location: "中转点", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "handoff-1",
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := domain.ReceiveBox(&batch, domain.ReceiveInput{BoxID: "box", Quantity: 1, Condition: domain.ConditionAccepted}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, err = ledger.BuildReceipt(batch, "卫生站", "receipt-1", now.Add(2*time.Minute))
	if !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("签收时间早于最后一条时间线事件应被拒绝: %v", err)
	}
}
