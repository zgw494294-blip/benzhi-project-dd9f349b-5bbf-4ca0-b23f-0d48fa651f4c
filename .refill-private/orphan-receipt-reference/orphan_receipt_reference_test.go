package orphanreceiptreference

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/repository"
)

func TestOpenRejectsOrphanReceiptReference(t *testing.T) {
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
	if err := domain.ReceiveBox(&batch, domain.ReceiveInput{BoxID: "box", Quantity: 1, Condition: domain.ConditionAccepted}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := domain.Close(&batch, "receipt-1", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	snapshot := repository.Snapshot{SchemaVersion: 1, Batches: map[string]domain.DeliveryBatch{batch.ID: batch}, Receipts: map[string]domain.ReceiptCredential{}}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := repository.Open(path)
	if !errors.Is(err, repository.ErrLedgerCorrupt) || store != nil {
		t.Fatalf("缺少凭据的关闭批次应使账本损坏: store=%v err=%v", store, err)
	}
}
