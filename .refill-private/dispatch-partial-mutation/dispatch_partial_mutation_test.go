package dispatchpartial

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func TestDispatchFailureLeavesDraftUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{
		{ID: "box-a", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
		{ID: "box-b", Label: "B", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	err = domain.Dispatch(&batch, map[string]string{"box-a": "seal-a", "unknown": "seal-x"}, now.Add(time.Minute))
	if !errors.Is(err, domain.ErrInvalidData) {
		t.Fatalf("错误封签应被拒绝: %v", err)
	}
	if batch.Status != domain.StatusDraft || batch.Boxes[0].SealCode != "" || !batch.Boxes[0].SealedAt.IsZero() {
		t.Fatalf("失败发运不应留下部分封签: %+v", batch)
	}
}
