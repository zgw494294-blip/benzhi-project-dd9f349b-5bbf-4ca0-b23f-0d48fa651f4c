package staleversioncallback

import (
	"context"
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/repository"
)

func TestUpdateBatchDoesNotInvokeMutationOnStaleVersion(t *testing.T) {
	store := repository.NewMemory()
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	called := false
	_, _, err = store.UpdateBatch(context.Background(), batch.ID, batch.Version-1, func(*domain.DeliveryBatch, *repository.Snapshot) (repository.Mutation, error) {
		called = true
		return repository.Mutation{Changed: true}, nil
	})
	if !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("过期版本应冲突: %v", err)
	}
	if called {
		t.Fatal("版本冲突时不应调用变更函数")
	}
}
