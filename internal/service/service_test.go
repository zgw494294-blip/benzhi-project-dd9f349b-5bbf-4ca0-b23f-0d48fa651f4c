package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/repository"
)

func newTestService() *Service {
	clockValue := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	return NewWithClock(repository.NewMemory(), func() time.Time { return clockValue })
}

func createTestBatch(t *testing.T, svc *Service) domain.DeliveryBatch {
	t.Helper()
	batch, err := svc.CreateBatch(context.Background(), CreateBatchRequest{RouteDate: "2026-08-19", Origin: "社区药房", Destination: "卫生站", Boxes: []domain.NewBoxInput{
		{ID: "box-a", Label: "A", DrugName: "疫苗", Quantity: 2, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
		{ID: "box-b", Label: "B", DrugName: "胰岛素", Quantity: 3, RequiredMinCelsius: 2, RequiredMaxCelsius: 8},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestServiceSupportsIdempotentHandoffAndVersionConflicts(t *testing.T) {
	svc := newTestService()
	created := createTestBatch(t, svc)
	dispatched, err := svc.Dispatch(context.Background(), created.ID, DispatchRequest{ExpectedVersion: created.Version, Seals: map[string]string{"box-a": "seal-a", "box-b": "seal-b"}})
	if err != nil {
		t.Fatal(err)
	}
	request := HandoffRequest{ExpectedVersion: dispatched.Version, FromParty: "社区药房", ToParty: "配送员", Location: "冷库", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "handoff-1"}
	first, err := svc.Handoff(context.Background(), created.ID, request)
	if err != nil || first.Batch.Version != 3 || first.Replayed {
		t.Fatalf("首次交接结果错误: %+v err=%v", first, err)
	}
	replay, err := svc.Handoff(context.Background(), created.ID, request)
	if err != nil || !replay.Replayed || replay.Batch.Version != 3 || replay.Event.ID != first.Event.ID {
		t.Fatalf("重放结果错误: %+v err=%v", replay, err)
	}
	request.Notes = "不同内容"
	if _, err := svc.Handoff(context.Background(), created.ID, request); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("幂等键复用错误 = %v", err)
	}
	if _, err := svc.Receive(context.Background(), created.ID, ReceiveRequest{ExpectedVersion: 2, BoxID: "box-a", Quantity: 2, Condition: domain.ConditionAccepted}); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期版本错误 = %v", err)
	}
}

func TestServiceClosesAndVerifiesReceipt(t *testing.T) {
	svc := newTestService()
	created := createTestBatch(t, svc)
	dispatched, err := svc.Dispatch(context.Background(), created.ID, DispatchRequest{ExpectedVersion: 1, Seals: map[string]string{"box-a": "seal-a", "box-b": "seal-b"}})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := svc.Handoff(context.Background(), created.ID, HandoffRequest{ExpectedVersion: dispatched.Version, FromParty: "社区药房", ToParty: "配送员", Location: "冷库", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "handoff-1"})
	if err != nil {
		t.Fatal(err)
	}
	partial, err := svc.Receive(context.Background(), created.ID, ReceiveRequest{ExpectedVersion: handoff.Batch.Version, BoxID: "box-a", Quantity: 2, Condition: domain.ConditionAccepted})
	if err != nil {
		t.Fatal(err)
	}
	complete, err := svc.Receive(context.Background(), created.ID, ReceiveRequest{ExpectedVersion: partial.Version, BoxID: "box-b", Quantity: 2, Condition: domain.ConditionException, ExceptionNote: "数量短缺"})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := svc.Close(context.Background(), created.ID, CloseRequest{ExpectedVersion: complete.Version, Receiver: "卫生站收货员"})
	if err != nil || closed.Batch.Status != domain.StatusClosed || closed.Batch.Version != complete.Version+1 {
		t.Fatalf("关闭结果错误: %+v err=%v", closed, err)
	}
	receipt, err := svc.Receipt(context.Background(), created.ID)
	if err != nil || receipt.ReceiptHash == "" || receipt.BoxResults[1].Quantity != 2 {
		t.Fatalf("凭据结果错误: %+v err=%v", receipt, err)
	}
	events, err := svc.Events(context.Background(), created.ID, "", 100)
	if err != nil || len(events.Events) != 6 {
		t.Fatalf("事件查询结果错误: %+v err=%v", events, err)
	}
}

func TestServiceReceivesBatchAtomically(t *testing.T) {
	svc := newTestService()
	created := createTestBatch(t, svc)
	dispatched, err := svc.Dispatch(context.Background(), created.ID, DispatchRequest{ExpectedVersion: created.Version, Seals: map[string]string{"box-a": "seal-a", "box-b": "seal-b"}})
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := svc.Handoff(context.Background(), created.ID, HandoffRequest{ExpectedVersion: dispatched.Version, FromParty: "社区药房", ToParty: "配送员", Location: "冷库", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "batch-receive-1"})
	if err != nil {
		t.Fatal(err)
	}
	invalidItems := ReceiveBatchRequest{ExpectedVersion: handoff.Batch.Version, Items: []domain.ReceiveInput{
		{BoxID: "box-a", Quantity: 2, Condition: domain.ConditionAccepted},
		{BoxID: "unknown", Quantity: 1, Condition: domain.ConditionAccepted},
	}}
	if _, err := svc.ReceiveBatch(context.Background(), created.ID, invalidItems); !errors.Is(err, ErrValidation) {
		t.Fatalf("整单校验错误 = %v", err)
	}
	unchanged, err := svc.GetBatch(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != handoff.Batch.Version || unchanged.Status != domain.StatusDispatched || !unchanged.Boxes[0].AcceptedAt.IsZero() || !unchanged.Boxes[1].AcceptedAt.IsZero() {
		t.Fatalf("失败整单不应产生部分写入: %+v", unchanged)
	}
	complete, err := svc.ReceiveBatch(context.Background(), created.ID, ReceiveBatchRequest{ExpectedVersion: handoff.Batch.Version, Items: []domain.ReceiveInput{
		{BoxID: "box-a", Quantity: 2, Condition: domain.ConditionAccepted},
		{BoxID: "box-b", Quantity: 2, Condition: domain.ConditionException, ExceptionNote: "数量短缺"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if complete.Version != handoff.Batch.Version+1 || complete.Status != domain.StatusReceived || complete.Boxes[1].ExceptionNote != "数量短缺" {
		t.Fatalf("批量验收结果错误: %+v", complete)
	}
	if _, err := svc.ReceiveBatch(context.Background(), created.ID, ReceiveBatchRequest{ExpectedVersion: handoff.Batch.Version, Items: invalidItems.Items}); !errors.Is(err, ErrConflict) {
		t.Fatalf("批量验收版本冲突错误 = %v", err)
	}
}

func TestServicePropagatesCanceledContext(t *testing.T) {
	svc := newTestService()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.CreateBatch(ctx, CreateBatchRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消上下文错误 = %v", err)
	}
}
