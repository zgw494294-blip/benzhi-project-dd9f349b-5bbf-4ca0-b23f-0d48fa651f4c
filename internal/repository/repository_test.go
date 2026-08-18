package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func testBatch(t *testing.T) domain.DeliveryBatch {
	t.Helper()
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestStorePersistsAndReloadsCommittedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBatch(context.Background(), testBatch(t)); err != nil {
		t.Fatal(err)
	}
	updated, _, err := store.UpdateBatch(context.Background(), "batch-1", 1, func(batch *domain.DeliveryBatch, _ *Snapshot) (Mutation, error) {
		if err := domain.Dispatch(batch, map[string]string{"box": "seal"}, batch.CreatedAt.Add(time.Minute)); err != nil {
			return Mutation{}, err
		}
		return Mutation{Changed: true}, nil
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("提交结果错误: version=%d err=%v", updated.Version, err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := reloaded.GetBatch(context.Background(), "batch-1")
	if err != nil || batch.Status != domain.StatusDispatched || batch.Version != 2 {
		t.Fatalf("重载结果错误: %+v err=%v", batch, err)
	}
	if _, _, err := reloaded.UpdateBatch(context.Background(), "batch-1", 1, func(batch *domain.DeliveryBatch, _ *Snapshot) (Mutation, error) {
		return Mutation{}, nil
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("版本冲突错误 = %v", err)
	}
}

func TestStoreRejectsCorruptLedgerAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99,"batches":{},"receipts":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrLedgerCorrupt) {
		t.Fatalf("损坏账本错误 = %v", err)
	}
	store := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.CreateBatch(ctx, testBatch(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误 = %v", err)
	}
}
