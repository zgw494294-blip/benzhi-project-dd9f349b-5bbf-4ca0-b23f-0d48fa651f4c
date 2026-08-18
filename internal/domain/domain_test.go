package domain

import (
	"errors"
	"testing"
	"time"
)

func TestBatchLifecycleValidatesStateAndCollections(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	batch, err := NewBatch("batch-1", "2026-08-19", "社区药房", "南区卫生站", []NewBoxInput{
		{ID: "box-a", Label: "A", DrugName: "疫苗", Quantity: 10, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
		{ID: "box-b", Label: "B", DrugName: "胰岛素", Quantity: 4, RequiredMinCelsius: 3, RequiredMaxCelsius: 7},
	}, now)
	if err != nil {
		t.Fatalf("创建批次失败: %v", err)
	}
	if err := Dispatch(&batch, map[string]string{"box-a": "seal-a", "box-b": "seal-b"}, now.Add(time.Minute)); err != nil {
		t.Fatalf("发运失败: %v", err)
	}
	if _, err := AppendHandoff(&batch, HandoffInput{FromParty: "社区药房", ToParty: "配送员", Location: "冷库", TemperatureCelsius: 3, Unit: "celsius", IdempotencyKey: "handoff-1"}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("交接失败: %v", err)
	}
	if err := ReceiveBox(&batch, ReceiveInput{BoxID: "box-a", Quantity: 10, Condition: ConditionAccepted}, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("首箱签收失败: %v", err)
	}
	if batch.Status != StatusReceivedPartial {
		t.Fatalf("首箱签收后状态 = %s", batch.Status)
	}
	if err := ReceiveBox(&batch, ReceiveInput{BoxID: "box-b", Quantity: 3, Condition: ConditionException, ExceptionNote: "外包装轻微破损"}, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("异常签收失败: %v", err)
	}
	if batch.Status != StatusReceived || batch.Boxes[1].ReceivedQuantity != 3 {
		t.Fatalf("完整签收状态或数量错误: status=%s quantity=%d", batch.Status, batch.Boxes[1].ReceivedQuantity)
	}
	if err := Close(&batch, "receipt-1", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	if batch.Status != StatusClosed || batch.ReceiptID != "receipt-1" {
		t.Fatalf("关闭结果错误: %+v", batch)
	}
}

func TestBatchRejectsInvalidCollectionsAndTemperature(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	_, err := NewBatch("batch-1", "2026-08-19", "A", "B", []NewBoxInput{
		{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
		{ID: "box", Label: "B", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
	}, now)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("重复药箱 ID 错误 = %v", err)
	}
	batch, err := NewBatch("batch-2", "2026-08-19", "A", "B", []NewBoxInput{{ID: "box", Label: "A", DrugName: "药品", Quantity: 1, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Dispatch(&batch, map[string]string{"box": "seal"}, now); err != nil {
		t.Fatal(err)
	}
	_, err = AppendHandoff(&batch, HandoffInput{FromParty: "A", ToParty: "B", Location: "中转点", TemperatureCelsius: 9, Unit: "C", IdempotencyKey: "key"}, now)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("超温错误 = %v", err)
	}
	_, err = AppendHandoff(&batch, HandoffInput{FromParty: "错误起点", ToParty: "B", Location: "中转点", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "key"}, now)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("错误链路错误 = %v", err)
	}
}
