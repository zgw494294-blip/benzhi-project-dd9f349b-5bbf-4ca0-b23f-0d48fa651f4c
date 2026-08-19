package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/ledger"
	"coldchain-route-ledger/internal/repository"
)

var (
	ErrValidation        = errors.New("service validation failed")
	ErrNotFound          = errors.New("service item not found")
	ErrConflict          = errors.New("service conflict")
	ErrIdempotency       = errors.New("idempotency conflict")
	ErrCorruptCredential = errors.New("stored receipt is invalid")
)

type Service struct {
	repo  *repository.Store
	clock func() time.Time
}

type CreateBatchRequest struct {
	RouteDate   string
	Origin      string
	Destination string
	Boxes       []domain.NewBoxInput
}

type BatchSearchRequest struct {
	Status      string
	RouteDate   string
	Origin      string
	Destination string
	Limit       int
	Offset      int
}

type DispatchRequest struct {
	ExpectedVersion int
	Seals           map[string]string
}

type HandoffRequest struct {
	ExpectedVersion    int
	FromParty          string
	ToParty            string
	OccurredAt         time.Time
	Location           string
	TemperatureCelsius float64
	Unit               string
	Notes              string
	IdempotencyKey     string
}

type ReceiveRequest struct {
	ExpectedVersion int
	BoxID           string
	Quantity        int
	Condition       string
	ExceptionNote   string
}

type ReceiveBatchRequest struct {
	ExpectedVersion int
	Items           []domain.ReceiveInput
}

type CloseRequest struct {
	ExpectedVersion int
	Receiver        string
}

type HandoffResult struct {
	Batch    domain.DeliveryBatch `json:"batch"`
	Event    domain.HandoffEvent  `json:"event"`
	Replayed bool                 `json:"replayed"`
}

type ReceiptResult struct {
	Batch   domain.DeliveryBatch     `json:"batch"`
	Receipt domain.ReceiptCredential `json:"receipt"`
}

func New(repo *repository.Store) *Service {
	return &Service{repo: repo, clock: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }}
}

func NewWithClock(repo *repository.Store, clock func() time.Time) *Service {
	if clock == nil {
		return New(repo)
	}
	return &Service{repo: repo, clock: clock}
}

func (s *Service) CreateBatch(ctx context.Context, request CreateBatchRequest) (domain.DeliveryBatch, error) {
	if err := contextError(ctx); err != nil {
		return domain.DeliveryBatch{}, err
	}
	id, err := newID("batch")
	if err != nil {
		return domain.DeliveryBatch{}, err
	}
	batch, err := domain.NewBatch(id, request.RouteDate, request.Origin, request.Destination, request.Boxes, s.clock())
	if err != nil {
		return domain.DeliveryBatch{}, wrapValidation(err)
	}
	if err := s.repo.CreateBatch(ctx, batch); err != nil {
		return domain.DeliveryBatch{}, mapRepositoryError(err)
	}
	return batch, nil
}

func (s *Service) Dispatch(ctx context.Context, id string, request DispatchRequest) (domain.DeliveryBatch, error) {
	if err := validateVersion(request.ExpectedVersion); err != nil {
		return domain.DeliveryBatch{}, err
	}
	result, _, err := s.repo.UpdateBatch(ctx, id, request.ExpectedVersion, func(batch *domain.DeliveryBatch, _ *repository.Snapshot) (repository.Mutation, error) {
		if err := contextError(ctx); err != nil {
			return repository.Mutation{}, err
		}
		if request.ExpectedVersion != batch.Version {
			return repository.Mutation{}, conflict(request.ExpectedVersion, batch.Version)
		}
		if err := domain.Dispatch(batch, request.Seals, s.clock()); err != nil {
			return repository.Mutation{}, wrapDomainError(err)
		}
		return repository.Mutation{Changed: true}, nil
	})
	if err != nil {
		return domain.DeliveryBatch{}, mapRepositoryError(err)
	}
	return result, nil
}

func (s *Service) Handoff(ctx context.Context, id string, request HandoffRequest) (HandoffResult, error) {
	if err := validateVersion(request.ExpectedVersion); err != nil {
		return HandoffResult{}, err
	}
	var output HandoffResult
	result, value, err := s.repo.UpdateBatch(ctx, id, request.ExpectedVersion, func(batch *domain.DeliveryBatch, _ *repository.Snapshot) (repository.Mutation, error) {
		if err := contextError(ctx); err != nil {
			return repository.Mutation{}, err
		}
		if existing, ok := findHandoff(batch.Handoffs, request.IdempotencyKey); ok {
			if sameHandoff(existing, request) {
				output = HandoffResult{Batch: *batch, Event: existing, Replayed: true}
				return repository.Mutation{IgnoreVersion: true, Result: output}, nil
			}
			return repository.Mutation{}, fmt.Errorf("%w: 幂等键已绑定其他交接内容", ErrIdempotency)
		}
		if request.ExpectedVersion != batch.Version {
			return repository.Mutation{}, conflict(request.ExpectedVersion, batch.Version)
		}
		event, err := domain.AppendHandoff(batch, domain.HandoffInput{BatchID: id, FromParty: request.FromParty, ToParty: request.ToParty, OccurredAt: request.OccurredAt, Location: request.Location, TemperatureCelsius: request.TemperatureCelsius, Unit: request.Unit, Notes: request.Notes, IdempotencyKey: request.IdempotencyKey}, s.clock())
		if err != nil {
			return repository.Mutation{}, wrapDomainError(err)
		}
		output = HandoffResult{Batch: *batch, Event: event}
		return repository.Mutation{Changed: true, Result: output}, nil
	})
	if err != nil {
		return HandoffResult{}, mapRepositoryError(err)
	}
	if value != nil {
		if replay, ok := value.(HandoffResult); ok {
			replay.Batch = result
			return replay, nil
		}
	}
	output.Batch = result
	return output, nil
}

func (s *Service) Receive(ctx context.Context, id string, request ReceiveRequest) (domain.DeliveryBatch, error) {
	if err := validateVersion(request.ExpectedVersion); err != nil {
		return domain.DeliveryBatch{}, err
	}
	result, _, err := s.repo.UpdateBatch(ctx, id, request.ExpectedVersion, func(batch *domain.DeliveryBatch, _ *repository.Snapshot) (repository.Mutation, error) {
		if err := contextError(ctx); err != nil {
			return repository.Mutation{}, err
		}
		if request.ExpectedVersion != batch.Version {
			return repository.Mutation{}, conflict(request.ExpectedVersion, batch.Version)
		}
		if err := domain.ReceiveBox(batch, domain.ReceiveInput{BoxID: request.BoxID, Quantity: request.Quantity, Condition: request.Condition, ExceptionNote: request.ExceptionNote}, s.clock()); err != nil {
			return repository.Mutation{}, wrapDomainError(err)
		}
		return repository.Mutation{Changed: true}, nil
	})
	if err != nil {
		return domain.DeliveryBatch{}, mapRepositoryError(err)
	}
	return result, nil
}

func (s *Service) ReceiveBatch(ctx context.Context, id string, request ReceiveBatchRequest) (domain.DeliveryBatch, error) {
	if err := validateVersion(request.ExpectedVersion); err != nil {
		return domain.DeliveryBatch{}, err
	}
	result, _, err := s.repo.UpdateBatch(ctx, id, request.ExpectedVersion, func(batch *domain.DeliveryBatch, _ *repository.Snapshot) (repository.Mutation, error) {
		if err := contextError(ctx); err != nil {
			return repository.Mutation{}, err
		}
		if request.ExpectedVersion != batch.Version {
			return repository.Mutation{}, conflict(request.ExpectedVersion, batch.Version)
		}
		if err := domain.ReceiveBoxes(batch, request.Items, s.clock()); err != nil {
			return repository.Mutation{}, wrapDomainError(err)
		}
		return repository.Mutation{Changed: true}, nil
	})
	if err != nil {
		return domain.DeliveryBatch{}, mapRepositoryError(err)
	}
	return result, nil
}

func (s *Service) Close(ctx context.Context, id string, request CloseRequest) (ReceiptResult, error) {
	if err := validateVersion(request.ExpectedVersion); err != nil {
		return ReceiptResult{}, err
	}
	var output ReceiptResult
	result, _, err := s.repo.UpdateBatch(ctx, id, request.ExpectedVersion, func(batch *domain.DeliveryBatch, snapshot *repository.Snapshot) (repository.Mutation, error) {
		if err := contextError(ctx); err != nil {
			return repository.Mutation{}, err
		}
		if request.ExpectedVersion != batch.Version {
			return repository.Mutation{}, conflict(request.ExpectedVersion, batch.Version)
		}
		receiptID, err := newID("receipt")
		if err != nil {
			return repository.Mutation{}, err
		}
		receipt, err := ledger.BuildReceipt(*batch, request.Receiver, receiptID, s.clock())
		if err != nil {
			return repository.Mutation{}, wrapDomainError(err)
		}
		if err := domain.Close(batch, receipt.ID, s.clock()); err != nil {
			return repository.Mutation{}, wrapDomainError(err)
		}
		if _, exists := snapshot.Receipts[receipt.ID]; exists {
			return repository.Mutation{}, fmt.Errorf("%w: 凭据 ID 已存在", ErrConflict)
		}
		snapshot.Receipts[receipt.ID] = receipt
		output = ReceiptResult{Batch: *batch, Receipt: receipt}
		return repository.Mutation{Changed: true, Result: output}, nil
	})
	if err != nil {
		return ReceiptResult{}, mapRepositoryError(err)
	}
	output.Batch = result
	return output, nil
}

func (s *Service) GetBatch(ctx context.Context, id string) (domain.DeliveryBatch, error) {
	batch, err := s.repo.GetBatch(ctx, id)
	if err != nil {
		return domain.DeliveryBatch{}, mapRepositoryError(err)
	}
	return batch, nil
}

func (s *Service) SearchBatches(ctx context.Context, request BatchSearchRequest) (repository.BatchPage, error) {
	if err := contextError(ctx); err != nil {
		return repository.BatchPage{}, err
	}
	if request.Status != "" && !domain.IsBatchStatus(request.Status) {
		return repository.BatchPage{}, wrapValidation(fmt.Errorf("status 不受支持: %s", request.Status))
	}
	if request.RouteDate != "" {
		if err := domain.ValidateRouteDate(request.RouteDate); err != nil {
			return repository.BatchPage{}, wrapValidation(err)
		}
	}
	limit := request.Limit
	if limit == 0 {
		limit = repository.DefaultBatchQueryLimit
	}
	if limit < 1 || limit > repository.MaxBatchQueryLimit {
		return repository.BatchPage{}, wrapValidation(fmt.Errorf("limit 必须在 1 到 %d 之间", repository.MaxBatchQueryLimit))
	}
	if request.Offset < 0 {
		return repository.BatchPage{}, wrapValidation(fmt.Errorf("offset 不能为负数"))
	}
	page, err := s.repo.SearchBatches(ctx, repository.BatchQuery{Status: request.Status, RouteDate: request.RouteDate, Origin: request.Origin, Destination: request.Destination, Limit: limit, Offset: request.Offset})
	if err != nil {
		return repository.BatchPage{}, mapRepositoryError(err)
	}
	return page, nil
}

func (s *Service) Events(ctx context.Context, id, cursor string, limit int) (ledger.EventPage, error) {
	batch, err := s.GetBatch(ctx, id)
	if err != nil {
		return ledger.EventPage{}, err
	}
	var receipt *domain.ReceiptCredential
	if batch.ReceiptID != "" {
		stored, err := s.repo.GetReceipt(ctx, batch.ReceiptID)
		if err != nil {
			return ledger.EventPage{}, mapRepositoryError(err)
		}
		receipt = &stored
	}
	page, err := ledger.Page(ledger.Timeline(batch, receipt), cursor, limit)
	if err != nil {
		return ledger.EventPage{}, wrapValidation(err)
	}
	return page, nil
}

func (s *Service) Receipt(ctx context.Context, id string) (domain.ReceiptCredential, error) {
	batch, err := s.GetBatch(ctx, id)
	if err != nil {
		return domain.ReceiptCredential{}, err
	}
	if batch.ReceiptID == "" {
		return domain.ReceiptCredential{}, fmt.Errorf("%w: 批次尚未关闭", ErrNotFound)
	}
	receipt, err := s.repo.GetReceipt(ctx, batch.ReceiptID)
	if err != nil {
		return domain.ReceiptCredential{}, mapRepositoryError(err)
	}
	if err := ledger.VerifyReceipt(batch, receipt); err != nil {
		return domain.ReceiptCredential{}, fmt.Errorf("%w: %v", ErrCorruptCredential, err)
	}
	return receipt, nil
}

func RunSelfCheck(ctx context.Context) error {
	service := New(repository.NewMemory())
	date := service.clock().Format("2006-01-02")
	created, err := service.CreateBatch(ctx, CreateBatchRequest{RouteDate: date, Origin: "社区药房", Destination: "南区卫生站", Boxes: []domain.NewBoxInput{{ID: "box-a", Label: "A01", DrugName: "冷藏疫苗", Quantity: 10, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}, {ID: "box-b", Label: "B01", DrugName: "胰岛素", Quantity: 6, RequiredMinCelsius: 2, RequiredMaxCelsius: 8}}})
	if err != nil {
		return err
	}
	dispatched, err := service.Dispatch(ctx, created.ID, DispatchRequest{ExpectedVersion: created.Version, Seals: map[string]string{"box-a": "seal-a", "box-b": "seal-b"}})
	if err != nil {
		return err
	}
	handoff, err := service.Handoff(ctx, created.ID, HandoffRequest{ExpectedVersion: dispatched.Version, FromParty: "社区药房", ToParty: "配送员", Location: "药房冷库", TemperatureCelsius: 5, Unit: "C", IdempotencyKey: "selfcheck-1"})
	if err != nil {
		return err
	}
	received, err := service.Receive(ctx, created.ID, ReceiveRequest{ExpectedVersion: handoff.Batch.Version, BoxID: "box-a", Quantity: 10, Condition: domain.ConditionAccepted})
	if err != nil {
		return err
	}
	received, err = service.Receive(ctx, created.ID, ReceiveRequest{ExpectedVersion: received.Version, BoxID: "box-b", Quantity: 6, Condition: domain.ConditionAccepted})
	if err != nil {
		return err
	}
	closed, err := service.Close(ctx, created.ID, CloseRequest{ExpectedVersion: received.Version, Receiver: "南区卫生站收货员"})
	if err != nil {
		return err
	}
	if _, err := service.Receipt(ctx, closed.Batch.ID); err != nil {
		return err
	}
	return nil
}

func findHandoff(events []domain.HandoffEvent, key string) (domain.HandoffEvent, bool) {
	for _, event := range events {
		if event.IdempotencyKey == key {
			return event, true
		}
	}
	return domain.HandoffEvent{}, false
}

func sameHandoff(event domain.HandoffEvent, request HandoffRequest) bool {
	fromParty := strings.TrimSpace(request.FromParty)
	toParty := strings.TrimSpace(request.ToParty)
	location := strings.TrimSpace(request.Location)
	notes := strings.TrimSpace(request.Notes)
	unit, err := domain.NormalizeUnit(request.Unit)
	if err != nil {
		return false
	}
	occurredAt := request.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = event.OccurredAt
	}
	return event.FromParty == fromParty && event.ToParty == toParty && event.Location == location && event.TemperatureCelsius == request.TemperatureCelsius && event.Unit == unit && event.Notes == notes && event.IdempotencyKey == request.IdempotencyKey && event.OccurredAt.Equal(occurredAt.UTC().Truncate(time.Microsecond))
}

func validateVersion(version int) error {
	if version < 1 {
		return wrapValidation(fmt.Errorf("expectedVersion 必须为正数"))
	}
	return nil
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func conflict(expected, actual int) error {
	return fmt.Errorf("%w: expectedVersion=%d，当前版本=%d", ErrConflict, expected, actual)
}

func wrapValidation(err error) error {
	return fmt.Errorf("%w: %v", ErrValidation, err)
}

func wrapDomainError(err error) error {
	if errors.Is(err, domain.ErrInvalidData) || errors.Is(err, domain.ErrInvalidState) {
		return wrapValidation(err)
	}
	return err
}

func mapRepositoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, repository.ErrVersionConflict):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	case errors.Is(err, repository.ErrAlreadyExists):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	default:
		return err
	}
}

func newID(prefix string) (string, error) {
	bytes := make([]byte, 10)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("生成 %s ID 失败: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(bytes), nil
}
