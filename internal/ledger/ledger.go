package ledger

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"coldchain-route-ledger/internal/domain"
)

var ErrInvalidCursor = errors.New("invalid event cursor")

type AuditEvent struct {
	ID         string            `json:"id"`
	BatchID    string            `json:"batchID"`
	Type       string            `json:"type"`
	OccurredAt time.Time         `json:"occurredAt"`
	Sequence   int               `json:"sequence"`
	Summary    string            `json:"summary"`
	Data       map[string]string `json:"data,omitempty"`
}

type EventPage struct {
	Events     []AuditEvent `json:"events"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

func Timeline(batch domain.DeliveryBatch, receipt *domain.ReceiptCredential) []AuditEvent {
	return timelineAt(batch, receipt)
}

func TimelineAtClose(batch domain.DeliveryBatch, receiver string, signedAt time.Time) []AuditEvent {
	return timelineAt(batch, &domain.ReceiptCredential{ID: batch.ID + "-closing", BatchID: batch.ID, Receiver: receiver, SignedAt: signedAt})
}

func timelineForReceipt(batch domain.DeliveryBatch, receipt domain.ReceiptCredential) []AuditEvent {
	return timelineAt(batch, &receipt)
}

func BuildReceipt(batch domain.DeliveryBatch, receiver, receiptID string, signedAt time.Time) (domain.ReceiptCredential, error) {
	if err := domain.ValidateBatch(batch); err != nil {
		return domain.ReceiptCredential{}, err
	}
	if batch.Status != domain.StatusReceived {
		return domain.ReceiptCredential{}, fmt.Errorf("%w: 只有已全部签收批次可以生成凭据", domain.ErrInvalidState)
	}
	if strings.TrimSpace(receiver) == "" || strings.TrimSpace(receiptID) == "" {
		return domain.ReceiptCredential{}, fmt.Errorf("%w: receiver 和 receiptID 不能为空", domain.ErrInvalidData)
	}
	signedAt = signedAt.UTC().Truncate(time.Microsecond)
	timeline := timelineAt(batch, &domain.ReceiptCredential{ID: receiptID, BatchID: batch.ID, Receiver: strings.TrimSpace(receiver), SignedAt: signedAt})
	timelineDigest, err := digestJSON(timeline)
	if err != nil {
		return domain.ReceiptCredential{}, err
	}
	results := make([]domain.ReceiptBoxResult, 0, len(batch.Boxes))
	for _, box := range batch.Boxes {
		results = append(results, domain.ReceiptBoxResult{BoxID: box.ID, Quantity: box.ReceivedQuantity, Condition: box.Condition, AcceptedAt: box.AcceptedAt, ExceptionNote: box.ExceptionNote})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].BoxID < results[j].BoxID })
	receipt := domain.ReceiptCredential{ID: strings.TrimSpace(receiptID), BatchID: batch.ID, Receiver: strings.TrimSpace(receiver), SignedAt: signedAt, BoxResults: results, TimelineDigest: timelineDigest}
	hashValue, err := receiptHash(receipt)
	if err != nil {
		return domain.ReceiptCredential{}, err
	}
	receipt.ReceiptHash = hashValue
	return receipt, domain.ValidateReceipt(receipt)
}

func VerifyReceipt(batch domain.DeliveryBatch, receipt domain.ReceiptCredential) error {
	if err := domain.ValidateReceipt(receipt); err != nil {
		return err
	}
	if batch.ID != receipt.BatchID || batch.ReceiptID != receipt.ID || batch.Status != domain.StatusClosed {
		return fmt.Errorf("%w: 凭据与批次不匹配", domain.ErrInvalidData)
	}
	if err := verifyBoxResults(batch, receipt); err != nil {
		return err
	}
	timelineDigest, err := digestJSON(timelineForReceipt(batch, receipt))
	if err != nil {
		return err
	}
	if timelineDigest != receipt.TimelineDigest {
		return fmt.Errorf("%w: 时间线摘要不匹配", domain.ErrInvalidData)
	}
	expectedHash, err := receiptHash(receipt)
	if err != nil {
		return err
	}
	if expectedHash != receipt.ReceiptHash {
		return fmt.Errorf("%w: 凭据摘要不匹配", domain.ErrInvalidData)
	}
	return nil
}

func verifyBoxResults(batch domain.DeliveryBatch, receipt domain.ReceiptCredential) error {
	if len(batch.Boxes) != len(receipt.BoxResults) {
		return fmt.Errorf("%w: 凭据药箱集合不完整", domain.ErrInvalidData)
	}
	results := make(map[string]domain.ReceiptBoxResult, len(receipt.BoxResults))
	for _, result := range receipt.BoxResults {
		results[result.BoxID] = result
	}
	for _, box := range batch.Boxes {
		result, ok := results[box.ID]
		if !ok || result.Quantity != box.ReceivedQuantity || result.Condition != box.Condition || !result.AcceptedAt.Equal(box.AcceptedAt) || result.ExceptionNote != box.ExceptionNote {
			return fmt.Errorf("%w: 凭据药箱结果与批次不一致", domain.ErrInvalidData)
		}
	}
	return nil
}

func Page(events []AuditEvent, cursor string, limit int) (EventPage, error) {
	if limit <= 0 || limit > 100 {
		return EventPage{}, fmt.Errorf("%w: limit 必须在 1 到 100 之间", ErrInvalidCursor)
	}
	start, err := decodeCursor(cursor)
	if err != nil {
		return EventPage{}, err
	}
	if start > len(events) {
		return EventPage{}, fmt.Errorf("%w: 游标超出时间线范围", ErrInvalidCursor)
	}
	end := start + limit
	if end > len(events) {
		end = len(events)
	}
	result := EventPage{Events: append([]AuditEvent(nil), events[start:end]...)}
	if end < len(events) {
		result.NextCursor = encodeCursor(end)
	}
	return result, nil
}

func timelineAt(batch domain.DeliveryBatch, receipt *domain.ReceiptCredential) []AuditEvent {
	events := make([]AuditEvent, 0, 2+len(batch.Handoffs)+len(batch.Boxes))
	events = append(events, AuditEvent{ID: batch.ID + "-created", BatchID: batch.ID, Type: "batch.created", OccurredAt: batch.CreatedAt.UTC(), Sequence: 1, Summary: "创建配送批次", Data: map[string]string{"origin": batch.Origin, "destination": batch.Destination, "routeDate": batch.RouteDate}})
	if len(batch.Boxes) > 0 && !batch.Boxes[0].SealedAt.IsZero() {
		sealedAt := batch.Boxes[0].SealedAt
		for _, box := range batch.Boxes[1:] {
			if box.SealedAt.Before(sealedAt) {
				sealedAt = box.SealedAt
			}
		}
		events = append(events, AuditEvent{ID: batch.ID + "-dispatched", BatchID: batch.ID, Type: "batch.dispatched", OccurredAt: sealedAt.UTC(), Sequence: 2, Summary: "药箱封签并发运", Data: map[string]string{"boxCount": strconv.Itoa(len(batch.Boxes))}})
	}
	for _, handoff := range batch.Handoffs {
		events = append(events, AuditEvent{ID: handoff.ID, BatchID: batch.ID, Type: "handoff.recorded", OccurredAt: handoff.OccurredAt.UTC(), Sequence: 100 + handoff.Sequence, Summary: "登记运输交接和温度", Data: map[string]string{"fromParty": handoff.FromParty, "toParty": handoff.ToParty, "location": handoff.Location, "temperatureCelsius": strconv.FormatFloat(handoff.TemperatureCelsius, 'f', -1, 64), "unit": handoff.Unit, "idempotencyKey": handoff.IdempotencyKey}})
	}
	for index, box := range batch.Boxes {
		if box.AcceptedAt.IsZero() {
			continue
		}
		events = append(events, AuditEvent{ID: batch.ID + "-received-" + box.ID, BatchID: batch.ID, Type: "box.received", OccurredAt: box.AcceptedAt.UTC(), Sequence: 200 + index, Summary: "完成药箱验收", Data: map[string]string{"boxID": box.ID, "condition": box.Condition, "quantity": strconv.Itoa(box.ReceivedQuantity), "exceptionNote": box.ExceptionNote}})
	}
	if receipt != nil && !receipt.SignedAt.IsZero() {
		events = append(events, AuditEvent{ID: receipt.ID, BatchID: batch.ID, Type: "batch.closed", OccurredAt: receipt.SignedAt.UTC(), Sequence: 300, Summary: "关闭批次并生成签收凭据", Data: map[string]string{"receiver": receipt.Receiver, "receiptID": receipt.ID}})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].OccurredAt.Equal(events[j].OccurredAt) {
			if events[i].Sequence == events[j].Sequence {
				return events[i].ID < events[j].ID
			}
			return events[i].Sequence < events[j].Sequence
		}
		return events[i].OccurredAt.Before(events[j].OccurredAt)
	})
	return events
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("规范化时间线失败: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func receiptHash(receipt domain.ReceiptCredential) (string, error) {
	unsigned := struct {
		ID             string                    `json:"id"`
		BatchID        string                    `json:"batchID"`
		Receiver       string                    `json:"receiver"`
		SignedAt       time.Time                 `json:"signedAt"`
		BoxResults     []domain.ReceiptBoxResult `json:"boxResults"`
		TimelineDigest string                    `json:"timelineDigest"`
	}{receipt.ID, receipt.BatchID, receipt.Receiver, receipt.SignedAt.UTC(), receipt.BoxResults, receipt.TimelineDigest}
	return digestJSON(unsigned)
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + strconv.Itoa(offset)))
}

func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: 编码无效", ErrInvalidCursor)
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 2 || parts[0] != "v1" {
		return 0, fmt.Errorf("%w: 版本无效", ErrInvalidCursor)
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: 偏移无效", ErrInvalidCursor)
	}
	return offset, nil
}
