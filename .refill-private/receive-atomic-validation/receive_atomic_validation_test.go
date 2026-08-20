package receiveatomicvalidation

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func TestReceiveValidationFailureDoesNotMutateBox(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
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
	batch.TemperatureReadings[0].Unit = "K"
	err = domain.ReceiveBox(&batch, domain.ReceiveInput{BoxID: "box", Quantity: 1, Condition: domain.ConditionAccepted}, now.Add(3*time.Minute))
	if !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("已有账本校验失败时应拒绝签收: %v", err)
	}
	if !batch.Boxes[0].AcceptedAt.IsZero() || batch.Boxes[0].ReceivedQuantity != 0 || batch.Status != domain.StatusDispatched {
		t.Fatalf("失败签收不应修改药箱或状态: %+v", batch)
	}
}
