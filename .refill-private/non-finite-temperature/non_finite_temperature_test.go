package nonfinitetemperature

import (
	"errors"
	"math"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func TestNewBatchRejectsNonFiniteTemperatureRange(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	_, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{{
		ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: math.NaN(), RequiredMaxCelsius: 8,
	}}, now)
	if !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("非有限温控范围应被拒绝: %v", err)
	}
}
