package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusDraft           = "Draft"
	StatusDispatched      = "Dispatched"
	StatusReceivedPartial = "ReceivedPartial"
	StatusReceived        = "Received"
	StatusClosed          = "Closed"
	UnitCelsius           = "C"
	ConditionAccepted     = "accepted"
	ConditionException    = "exception"
	ConditionRejected     = "rejected"
)

var (
	ErrInvalidState = errors.New("invalid state transition")
	ErrInvalidData  = errors.New("invalid domain data")
)

type DeliveryBatch struct {
	ID                  string               `json:"id"`
	RouteDate           string               `json:"routeDate"`
	Origin              string               `json:"origin"`
	Destination         string               `json:"destination"`
	Status              string               `json:"status"`
	Version             int                  `json:"version"`
	Boxes               []MedicineBox        `json:"boxes"`
	Handoffs            []HandoffEvent       `json:"handoffs"`
	TemperatureReadings []TemperatureReading `json:"temperatureReadings"`
	ReceiptID           string               `json:"receiptID,omitempty"`
	CreatedAt           time.Time            `json:"createdAt"`
	UpdatedAt           time.Time            `json:"updatedAt"`
}

type BatchSummary struct {
	ID            string    `json:"id"`
	RouteDate     string    `json:"routeDate"`
	Origin        string    `json:"origin"`
	Destination   string    `json:"destination"`
	Status        string    `json:"status"`
	Version       int       `json:"version"`
	TotalBoxes    int       `json:"totalBoxes"`
	ReceivedBoxes int       `json:"receivedBoxes"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type MedicineBox struct {
	ID                 string    `json:"id"`
	Label              string    `json:"label"`
	DrugName           string    `json:"drugName"`
	Quantity           int       `json:"quantity"`
	RequiredMinCelsius float64   `json:"requiredMinCelsius"`
	RequiredMaxCelsius float64   `json:"requiredMaxCelsius"`
	SealCode           string    `json:"sealCode,omitempty"`
	SealedAt           time.Time `json:"sealedAt,omitempty"`
	AcceptedAt         time.Time `json:"acceptedAt,omitempty"`
	ReceivedQuantity   int       `json:"receivedQuantity,omitempty"`
	Condition          string    `json:"condition,omitempty"`
	ExceptionNote      string    `json:"exceptionNote,omitempty"`
}

type HandoffEvent struct {
	ID                 string    `json:"id"`
	BatchID            string    `json:"batchID"`
	Sequence           int       `json:"sequence"`
	FromParty          string    `json:"fromParty"`
	ToParty            string    `json:"toParty"`
	OccurredAt         time.Time `json:"occurredAt"`
	Location           string    `json:"location"`
	TemperatureCelsius float64   `json:"temperatureCelsius"`
	Unit               string    `json:"unit"`
	Notes              string    `json:"notes,omitempty"`
	IdempotencyKey     string    `json:"idempotencyKey"`
}

type TemperatureReading struct {
	ID                 string    `json:"id"`
	HandoffID          string    `json:"handoffID"`
	RecordedAt         time.Time `json:"recordedAt"`
	TemperatureCelsius float64   `json:"temperatureCelsius"`
	Unit               string    `json:"unit"`
}

type ReceiptBoxResult struct {
	BoxID         string    `json:"boxID"`
	Quantity      int       `json:"quantity"`
	Condition     string    `json:"condition"`
	AcceptedAt    time.Time `json:"acceptedAt"`
	ExceptionNote string    `json:"exceptionNote,omitempty"`
}

type ReceiptCredential struct {
	ID             string             `json:"id"`
	BatchID        string             `json:"batchID"`
	Receiver       string             `json:"receiver"`
	SignedAt       time.Time          `json:"signedAt"`
	BoxResults     []ReceiptBoxResult `json:"boxResults"`
	TimelineDigest string             `json:"timelineDigest"`
	ReceiptHash    string             `json:"receiptHash"`
}

type NewBoxInput struct {
	ID                 string
	Label              string
	DrugName           string
	Quantity           int
	RequiredMinCelsius float64
	RequiredMaxCelsius float64
}

type HandoffInput struct {
	ID                 string
	BatchID            string
	FromParty          string
	ToParty            string
	OccurredAt         time.Time
	Location           string
	TemperatureCelsius float64
	Unit               string
	Notes              string
	IdempotencyKey     string
}

type ReceiveInput struct {
	BoxID         string
	Quantity      int
	Condition     string
	ExceptionNote string
}

func SummarizeBatch(batch DeliveryBatch) BatchSummary {
	receivedBoxes := 0
	for _, box := range batch.Boxes {
		if !box.AcceptedAt.IsZero() {
			receivedBoxes++
		}
	}
	return BatchSummary{
		ID:            batch.ID,
		RouteDate:     batch.RouteDate,
		Origin:        batch.Origin,
		Destination:   batch.Destination,
		Status:        batch.Status,
		Version:       batch.Version,
		TotalBoxes:    len(batch.Boxes),
		ReceivedBoxes: receivedBoxes,
		UpdatedAt:     batch.UpdatedAt,
	}
}

func ValidateRouteDate(routeDate string) error {
	if _, err := time.Parse("2006-01-02", routeDate); err != nil {
		return invalid("routeDate", "必须是 YYYY-MM-DD")
	}
	return nil
}

func IsBatchStatus(status string) bool {
	switch status {
	case StatusDraft, StatusDispatched, StatusReceivedPartial, StatusReceived, StatusClosed:
		return true
	default:
		return false
	}
}

func NewBatch(id, routeDate, origin, destination string, boxes []NewBoxInput, now time.Time) (DeliveryBatch, error) {
	if strings.TrimSpace(id) == "" {
		return DeliveryBatch{}, invalid("id", "不能为空")
	}
	if err := ValidateRouteDate(routeDate); err != nil {
		return DeliveryBatch{}, err
	}
	if strings.TrimSpace(origin) == "" || strings.TrimSpace(destination) == "" {
		return DeliveryBatch{}, invalid("route", "起点和终点不能为空")
	}
	if len(boxes) == 0 {
		return DeliveryBatch{}, invalid("boxes", "至少需要一个药箱")
	}
	result := DeliveryBatch{
		ID: id, RouteDate: routeDate, Origin: strings.TrimSpace(origin), Destination: strings.TrimSpace(destination),
		Status: StatusDraft, Version: 1, CreatedAt: normalizeTime(now), UpdatedAt: normalizeTime(now),
		Boxes: make([]MedicineBox, 0, len(boxes)), Handoffs: []HandoffEvent{}, TemperatureReadings: []TemperatureReading{},
	}
	seen := make(map[string]struct{}, len(boxes))
	for _, input := range boxes {
		box := MedicineBox{ID: strings.TrimSpace(input.ID), Label: strings.TrimSpace(input.Label), DrugName: strings.TrimSpace(input.DrugName), Quantity: input.Quantity, RequiredMinCelsius: input.RequiredMinCelsius, RequiredMaxCelsius: input.RequiredMaxCelsius}
		if err := validateNewBox(box); err != nil {
			return DeliveryBatch{}, err
		}
		if _, exists := seen[box.ID]; exists {
			return DeliveryBatch{}, invalid("boxes", "药箱 ID 不能重复")
		}
		seen[box.ID] = struct{}{}
		result.Boxes = append(result.Boxes, box)
	}
	if err := validateTemperatureIntersection(result.Boxes); err != nil {
		return DeliveryBatch{}, err
	}
	return result, nil
}

func Dispatch(batch *DeliveryBatch, seals map[string]string, now time.Time) error {
	if batch == nil {
		return invalid("batch", "批次不能为空")
	}
	if batch.Status != StatusDraft {
		return stateError("只有草稿批次可以发运")
	}
	if len(seals) != len(batch.Boxes) {
		return invalid("seals", "必须为每个药箱提供封签码")
	}
	for index := range batch.Boxes {
		box := &batch.Boxes[index]
		seal := strings.TrimSpace(seals[box.ID])
		if seal == "" {
			return invalid("seals", "封签码不能为空")
		}
		box.SealCode = seal
		box.SealedAt = normalizeTime(now)
	}
	batch.Status = StatusDispatched
	batch.UpdatedAt = normalizeTime(now)
	return ValidateBatch(*batch)
}

func AppendHandoff(batch *DeliveryBatch, input HandoffInput, now time.Time) (HandoffEvent, error) {
	if batch == nil {
		return HandoffEvent{}, invalid("batch", "批次不能为空")
	}
	if batch.Status != StatusDispatched {
		return HandoffEvent{}, stateError("只有已发运批次可以登记交接")
	}
	fromParty := strings.TrimSpace(input.FromParty)
	toParty := strings.TrimSpace(input.ToParty)
	location := strings.TrimSpace(input.Location)
	key := strings.TrimSpace(input.IdempotencyKey)
	if fromParty == "" || toParty == "" || location == "" || key == "" {
		return HandoffEvent{}, invalid("handoff", "参与方、地点和幂等键不能为空")
	}
	if fromParty == toParty {
		return HandoffEvent{}, invalid("handoff", "交接双方不能相同")
	}
	if input.BatchID != "" && input.BatchID != batch.ID {
		return HandoffEvent{}, invalid("batchID", "交接事件不属于当前批次")
	}
	if len(batch.Handoffs) == 0 {
		if fromParty != batch.Origin {
			return HandoffEvent{}, invalid("fromParty", "首个交接必须从配送起点开始")
		}
	} else if fromParty != batch.Handoffs[len(batch.Handoffs)-1].ToParty {
		return HandoffEvent{}, invalid("fromParty", "交接参与方未与上一节点衔接")
	}
	unit, err := NormalizeUnit(input.Unit)
	if err != nil {
		return HandoffEvent{}, err
	}
	if input.TemperatureCelsius < minTemperature(batch.Boxes) || input.TemperatureCelsius > maxTemperature(batch.Boxes) {
		return HandoffEvent{}, invalid("temperatureCelsius", "温度超出药箱允许范围")
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = now
	}
	occurredAt = normalizeTime(occurredAt)
	if !batch.CreatedAt.IsZero() && occurredAt.Before(batch.CreatedAt) {
		return HandoffEvent{}, invalid("occurredAt", "交接时间不能早于批次创建时间")
	}
	if len(batch.Handoffs) > 0 && occurredAt.Before(batch.Handoffs[len(batch.Handoffs)-1].OccurredAt) {
		return HandoffEvent{}, invalid("occurredAt", "交接时间不能倒退")
	}
	event := HandoffEvent{
		ID: fmt.Sprintf("%s-handoff-%d", batch.ID, len(batch.Handoffs)+1), BatchID: batch.ID, Sequence: len(batch.Handoffs) + 1,
		FromParty: fromParty, ToParty: toParty, OccurredAt: occurredAt, Location: location,
		TemperatureCelsius: input.TemperatureCelsius, Unit: unit, Notes: strings.TrimSpace(input.Notes), IdempotencyKey: key,
	}
	batch.Handoffs = append(batch.Handoffs, event)
	batch.TemperatureReadings = append(batch.TemperatureReadings, TemperatureReading{
		ID: event.ID + "-temperature", HandoffID: event.ID, RecordedAt: occurredAt, TemperatureCelsius: input.TemperatureCelsius, Unit: unit,
	})
	batch.UpdatedAt = normalizeTime(now)
	return event, ValidateBatch(*batch)
}

func ReceiveBox(batch *DeliveryBatch, input ReceiveInput, now time.Time) error {
	if batch == nil {
		return invalid("batch", "批次不能为空")
	}
	if batch.Status != StatusDispatched && batch.Status != StatusReceivedPartial {
		return stateError("当前批次不能签收")
	}
	if len(batch.Handoffs) == 0 {
		return stateError("签收前必须至少登记一条运输交接")
	}
	boxIndex := -1
	for index := range batch.Boxes {
		if batch.Boxes[index].ID == strings.TrimSpace(input.BoxID) {
			boxIndex = index
			break
		}
	}
	if boxIndex < 0 {
		return fmt.Errorf("%w: boxID 不存在", ErrInvalidData)
	}
	box := &batch.Boxes[boxIndex]
	normalized, err := validateReceiveInput(*box, input)
	if err != nil {
		return err
	}
	rollback := snapshotReceiveState(batch, boxIndex)
	box.AcceptedAt = normalizeTime(now)
	box.ReceivedQuantity = normalized.Quantity
	box.Condition = normalized.Condition
	box.ExceptionNote = normalized.ExceptionNote
	batch.Status = StatusReceivedPartial
	for _, candidate := range batch.Boxes {
		if candidate.AcceptedAt.IsZero() {
			batch.Status = StatusReceivedPartial
			batch.UpdatedAt = normalizeTime(now)
			if err := ValidateBatch(*batch); err != nil {
				rollback(batch)
				return err
			}
			return nil
		}
	}
	batch.Status = StatusReceived
	batch.UpdatedAt = normalizeTime(now)
	if err := ValidateBatch(*batch); err != nil {
		rollback(batch)
		return err
	}
	return nil
}

func ReceiveBoxes(batch *DeliveryBatch, inputs []ReceiveInput, now time.Time) error {
	if batch == nil {
		return invalid("batch", "批次不能为空")
	}
	if len(inputs) == 0 {
		return invalid("items", "至少需要一个药箱验收项")
	}
	if batch.Status != StatusDispatched && batch.Status != StatusReceivedPartial {
		return stateError("当前批次不能签收")
	}
	if len(batch.Handoffs) == 0 {
		return stateError("签收前必须至少登记一条运输交接")
	}
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		boxIndex := -1
		boxID := strings.TrimSpace(input.BoxID)
		for index := range batch.Boxes {
			if batch.Boxes[index].ID == boxID {
				boxIndex = index
				break
			}
		}
		if boxIndex < 0 {
			return fmt.Errorf("%w: boxID 不存在", ErrInvalidData)
		}
		if _, exists := seen[boxID]; exists {
			return invalid("items", "药箱 ID 不能重复")
		}
		seen[boxID] = struct{}{}
		if !batch.Boxes[boxIndex].AcceptedAt.IsZero() {
			return stateError("药箱不能重复签收")
		}
		if _, err := validateReceiveInput(batch.Boxes[boxIndex], input); err != nil {
			return err
		}
	}
	rollback := snapshotReceiveState(batch, -1)
	for _, input := range inputs {
		if err := ReceiveBox(batch, input, now); err != nil {
			rollback(batch)
			return err
		}
	}
	if err := ValidateBatch(*batch); err != nil {
		rollback(batch)
		return err
	}
	return nil
}

func Close(batch *DeliveryBatch, receiptID string, now time.Time) error {
	if batch == nil {
		return invalid("batch", "批次不能为空")
	}
	if batch.Status != StatusReceived {
		return stateError("只有全部签收的批次可以关闭")
	}
	if strings.TrimSpace(receiptID) == "" {
		return invalid("receiptID", "凭据 ID 不能为空")
	}
	for _, box := range batch.Boxes {
		if box.AcceptedAt.IsZero() {
			return stateError("仍有药箱未签收")
		}
	}
	batch.ReceiptID = receiptID
	batch.Status = StatusClosed
	batch.UpdatedAt = normalizeTime(now)
	return ValidateBatch(*batch)
}

func NormalizeUnit(unit string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "c", "celsius", "°c":
		return UnitCelsius, nil
	default:
		return "", invalid("unit", "温度单位必须是 C")
	}
}

func ValidateBatch(batch DeliveryBatch) error {
	if strings.TrimSpace(batch.ID) == "" || batch.Version < 1 {
		return invalid("batch", "批次 ID 或版本无效")
	}
	if batch.CreatedAt.IsZero() || batch.UpdatedAt.IsZero() || batch.UpdatedAt.Before(batch.CreatedAt) {
		return invalid("timestamps", "批次时间字段无效")
	}
	if batch.Status != StatusDraft && batch.Status != StatusDispatched && batch.Status != StatusReceivedPartial && batch.Status != StatusReceived && batch.Status != StatusClosed {
		return invalid("status", "批次状态无效")
	}
	if len(batch.Boxes) == 0 {
		return invalid("boxes", "批次必须包含药箱")
	}
	seenBoxes := make(map[string]struct{}, len(batch.Boxes))
	for _, box := range batch.Boxes {
		if err := validateNewBox(box); err != nil {
			return err
		}
		if _, exists := seenBoxes[box.ID]; exists {
			return invalid("boxes", "药箱 ID 不能重复")
		}
		seenBoxes[box.ID] = struct{}{}
		if batch.Status == StatusDraft && (!box.SealedAt.IsZero() || box.SealCode != "") {
			return invalid("boxes", "草稿不能包含封签")
		}
		if batch.Status != StatusDraft && (box.SealCode == "" || box.SealedAt.IsZero()) {
			return invalid("boxes", "已发运批次的每个药箱都必须封签")
		}
		if !box.AcceptedAt.IsZero() {
			if (box.Condition != ConditionAccepted && box.Condition != ConditionException && box.Condition != ConditionRejected) || box.ReceivedQuantity <= 0 || box.ReceivedQuantity > box.Quantity || box.AcceptedAt.Before(batch.CreatedAt) {
				return invalid("boxes", "签收结果不完整")
			}
		}
	}
	if err := validateTemperatureIntersection(batch.Boxes); err != nil {
		return err
	}
	seenKeys := make(map[string]struct{}, len(batch.Handoffs))
	for index, event := range batch.Handoffs {
		if event.BatchID != batch.ID || event.Sequence != index+1 || event.ID == "" || event.FromParty == "" || event.ToParty == "" || event.Location == "" || event.IdempotencyKey == "" {
			return invalid("handoffs", "交接事件链路不完整")
		}
		unit, err := NormalizeUnit(event.Unit)
		if err != nil || unit != event.Unit {
			return invalid("handoffs", "交接温度单位不一致")
		}
		if event.TemperatureCelsius < minTemperature(batch.Boxes) || event.TemperatureCelsius > maxTemperature(batch.Boxes) {
			return invalid("handoffs", "交接温度超出允许范围")
		}
		if _, exists := seenKeys[event.IdempotencyKey]; exists {
			return invalid("handoffs", "幂等键不能重复")
		}
		seenKeys[event.IdempotencyKey] = struct{}{}
		if index == 0 && event.FromParty != batch.Origin {
			return invalid("handoffs", "首个交接起点无效")
		}
		if index > 0 && batch.Handoffs[index-1].ToParty != event.FromParty {
			return invalid("handoffs", "交接链路中断")
		}
		if index > 0 && event.OccurredAt.Before(batch.Handoffs[index-1].OccurredAt) {
			return invalid("handoffs", "交接时间不能倒退")
		}
	}
	if len(batch.TemperatureReadings) != len(batch.Handoffs) {
		return invalid("temperatureReadings", "温度采样必须与交接一一对应")
	}
	for index, reading := range batch.TemperatureReadings {
		event := batch.Handoffs[index]
		if reading.HandoffID != event.ID || reading.Unit != UnitCelsius || reading.TemperatureCelsius != event.TemperatureCelsius {
			return invalid("temperatureReadings", "温度采样与交接不一致")
		}
	}
	if batch.Status == StatusReceived || batch.Status == StatusClosed {
		for _, box := range batch.Boxes {
			if box.AcceptedAt.IsZero() {
				return invalid("status", "全部签收状态必须有完整结果")
			}
		}
	}
	acceptedCount := 0
	for _, box := range batch.Boxes {
		if !box.AcceptedAt.IsZero() {
			acceptedCount++
		}
	}
	switch batch.Status {
	case StatusDraft:
		if len(batch.Handoffs) != 0 || len(batch.TemperatureReadings) != 0 || acceptedCount != 0 || batch.ReceiptID != "" {
			return invalid("status", "草稿不能包含运输或签收记录")
		}
	case StatusDispatched:
		if acceptedCount != 0 || batch.ReceiptID != "" {
			return invalid("status", "已发运批次不能包含签收或关闭凭据")
		}
	case StatusReceivedPartial:
		if acceptedCount == 0 || acceptedCount == len(batch.Boxes) || batch.ReceiptID != "" {
			return invalid("status", "部分签收状态不符合药箱集合")
		}
	case StatusReceived:
		if acceptedCount != len(batch.Boxes) || batch.ReceiptID != "" {
			return invalid("status", "已签收状态不能包含关闭凭据")
		}
	case StatusClosed:
		if acceptedCount != len(batch.Boxes) || batch.ReceiptID == "" {
			return invalid("status", "已关闭状态必须包含完整签收和凭据")
		}
	}
	if batch.Status == StatusClosed && batch.ReceiptID == "" {
		return invalid("receiptID", "关闭批次必须引用签收凭据")
	}
	return nil
}

func validateReceiveInput(box MedicineBox, input ReceiveInput) (ReceiveInput, error) {
	input.BoxID = strings.TrimSpace(input.BoxID)
	input.Condition = strings.ToLower(strings.TrimSpace(input.Condition))
	input.ExceptionNote = strings.TrimSpace(input.ExceptionNote)
	if input.Condition != ConditionAccepted && input.Condition != ConditionException && input.Condition != ConditionRejected {
		return ReceiveInput{}, invalid("condition", "必须是 accepted、exception 或 rejected")
	}
	if input.Quantity <= 0 || input.Quantity > box.Quantity {
		return ReceiveInput{}, invalid("quantity", "签收数量必须为正且不能超过发运数量")
	}
	if (input.Condition == ConditionException || input.Condition == ConditionRejected) && input.ExceptionNote == "" {
		return ReceiveInput{}, invalid("exceptionNote", "异常或拒收必须填写备注")
	}
	if input.Condition == ConditionAccepted && input.Quantity != box.Quantity {
		return ReceiveInput{}, invalid("quantity", "正常签收数量必须等于发运数量")
	}
	return input, nil
}

func ValidateReceipt(receipt ReceiptCredential) error {
	if receipt.ID == "" || receipt.BatchID == "" || receipt.Receiver == "" || receipt.SignedAt.IsZero() || receipt.TimelineDigest == "" || receipt.ReceiptHash == "" {
		return invalid("receipt", "签收凭据字段不完整")
	}
	if len(receipt.BoxResults) == 0 {
		return invalid("boxResults", "签收凭据必须包含药箱结果")
	}
	seen := make(map[string]struct{}, len(receipt.BoxResults))
	for _, result := range receipt.BoxResults {
		if result.BoxID == "" || result.Quantity <= 0 || (result.Condition != ConditionAccepted && result.Condition != ConditionException && result.Condition != ConditionRejected) || result.AcceptedAt.IsZero() {
			return invalid("boxResults", "签收凭据药箱结果无效")
		}
		if _, exists := seen[result.BoxID]; exists {
			return invalid("boxResults", "签收凭据不能重复记录药箱")
		}
		seen[result.BoxID] = struct{}{}
	}
	return nil
}

func validateNewBox(box MedicineBox) error {
	if box.ID == "" || box.Label == "" || box.DrugName == "" {
		return invalid("box", "药箱 ID、标签和药品名不能为空")
	}
	if box.Quantity <= 0 {
		return invalid("quantity", "药箱数量必须为正")
	}
	if box.RequiredMinCelsius > box.RequiredMaxCelsius {
		return invalid("temperatureRange", "温控下限不能高于上限")
	}
	return nil
}

func validateTemperatureIntersection(boxes []MedicineBox) error {
	if minTemperature(boxes) > maxTemperature(boxes) {
		return invalid("temperatureRange", "所有药箱必须存在共同的运输温区")
	}
	return nil
}

func minTemperature(boxes []MedicineBox) float64 {
	value := boxes[0].RequiredMinCelsius
	for _, box := range boxes[1:] {
		if box.RequiredMinCelsius > value {
			value = box.RequiredMinCelsius
		}
	}
	return value
}

func maxTemperature(boxes []MedicineBox) float64 {
	value := boxes[0].RequiredMaxCelsius
	for _, box := range boxes[1:] {
		if box.RequiredMaxCelsius < value {
			value = box.RequiredMaxCelsius
		}
	}
	return value
}

func invalid(field, message string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidData, field, message)
}

func stateError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, message)
}

func normalizeTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

// snapshotReceiveState captures the mutable fields ReceiveBox/ReceiveBoxes
// touch so callers can restore them when the final ValidateBatch fails. A
// boxIndex of -1 means "all boxes" (used by ReceiveBoxes). Returning a
// closure keeps the rollback logic close to the mutation site and makes it
// obvious that the same fields are restored regardless of which validation
// path triggered the error.
func snapshotReceiveState(batch *DeliveryBatch, boxIndex int) func(*DeliveryBatch) {
	if batch == nil {
		return func(*DeliveryBatch) {}
	}
	originalStatus := batch.Status
	originalUpdatedAt := batch.UpdatedAt
	originalBoxes := make([]MedicineBox, len(batch.Boxes))
	copy(originalBoxes, batch.Boxes)
	return func(target *DeliveryBatch) {
		if target == nil {
			return
		}
		target.Status = originalStatus
		target.UpdatedAt = originalUpdatedAt
		if boxIndex >= 0 && boxIndex < len(target.Boxes) && boxIndex < len(originalBoxes) {
			target.Boxes[boxIndex] = originalBoxes[boxIndex]
			return
		}
		if len(originalBoxes) == len(target.Boxes) {
			copy(target.Boxes, originalBoxes)
		}
	}
}
