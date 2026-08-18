package ledger

import (
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
)

func receivedBatch(t *testing.T) domain.DeliveryBatch {
	t.Helper()
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	batch, err := domain.NewBatch("batch-1", "2026-08-19", "A", "B", []domain.NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 2, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Dispatch(&batch, map[string]string{"box": "seal"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.AppendHandoff(&batch, domain.HandoffInput{FromParty: "A", ToParty: "B", Location: "中转点", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "key"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := domain.ReceiveBox(&batch, domain.ReceiveInput{BoxID: "box", Quantity: 2, Condition: domain.ConditionAccepted}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestReceiptDigestIsStableAndVerifiable(t *testing.T) {
	batch := receivedBatch(t)
	signedAt := batch.UpdatedAt.Add(time.Minute)
	receipt, err := BuildReceipt(batch, "收货员", "receipt-1", signedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Close(&batch, receipt.ID, signedAt); err != nil {
		t.Fatal(err)
	}
	if err := VerifyReceipt(batch, receipt); err != nil {
		t.Fatalf("凭据校验失败: %v", err)
	}
	if len(Timeline(batch, &receipt)) != 5 {
		t.Fatalf("时间线长度错误: %d", len(Timeline(batch, &receipt)))
	}
	changed := receipt
	changed.Receiver = "另一位收货员"
	if err := VerifyReceipt(batch, changed); err == nil {
		t.Fatal("修改凭据后仍然校验成功")
	}
}

func TestEventPageRejectsInvalidCursor(t *testing.T) {
	page, err := Page([]AuditEvent{{ID: "a"}}, "invalid", 20)
	if !errors.Is(err, ErrInvalidCursor) || len(page.Events) != 0 {
		t.Fatalf("非法游标结果错误: page=%+v err=%v", page, err)
	}
	page, err = Page([]AuditEvent{{ID: "a"}, {ID: "b"}}, "", 1)
	if err != nil || len(page.Events) != 1 || page.NextCursor == "" {
		t.Fatalf("分页结果错误: %+v err=%v", page, err)
	}
	page, err = Page([]AuditEvent{{ID: "a"}, {ID: "b"}}, page.NextCursor, 1)
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != "b" || page.NextCursor != "" {
		t.Fatalf("第二页结果错误: %+v err=%v", page, err)
	}
}
