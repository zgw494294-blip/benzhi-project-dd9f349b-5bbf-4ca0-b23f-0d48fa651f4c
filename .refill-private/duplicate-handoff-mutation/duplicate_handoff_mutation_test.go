package duplicatehandoffmutation

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func TestAppendHandoffDuplicateKeyDoesNotMutateBatch(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "C", []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Dispatch(&batch, map[string]string{"box": "seal"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.AppendHandoff(&batch, domain.HandoffInput{FromParty: "A", ToParty: "B", Location: "点1", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "same"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	err = func() error {
		_, err := domain.AppendHandoff(&batch, domain.HandoffInput{FromParty: "B", ToParty: "C", Location: "点2", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "same"}, now.Add(3*time.Minute))
		return err
	}()
	if !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("重复幂等键应被拒绝: %v", err)
	}
	if len(batch.Handoffs) != 1 || len(batch.TemperatureReadings) != 1 {
		t.Fatalf("失败交接不应追加记录: handoffs=%d readings=%d", len(batch.Handoffs), len(batch.TemperatureReadings))
	}
}
