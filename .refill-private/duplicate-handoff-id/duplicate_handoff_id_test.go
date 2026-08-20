package duplicatehandoffid

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func TestValidateBatchRejectsDuplicateHandoffIDs(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "C", []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Dispatch(&batch, map[string]string{"box": "seal"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	first, err := domain.AppendHandoff(&batch, domain.HandoffInput{FromParty: "A", ToParty: "B", Location: "点1", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "key-1"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Sequence = 2
	second.FromParty = "B"
	second.ToParty = "C"
	second.Location = "点2"
	second.OccurredAt = now.Add(3 * time.Minute)
	second.IdempotencyKey = "key-2"
	batch.Handoffs = append(batch.Handoffs, second)
	batch.TemperatureReadings = append(batch.TemperatureReadings, domain.TemperatureReading{ID: second.ID + "-temperature", HandoffID: second.ID, RecordedAt: second.OccurredAt, TemperatureCelsius: second.TemperatureCelsius, Unit: second.Unit})
	if err := domain.ValidateBatch(batch); !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("重复交接 ID 应被拒绝: %v", err)
	}
}
