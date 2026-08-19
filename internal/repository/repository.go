package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"coldchain-route-ledger/internal/domain"
)

const currentSchemaVersion = 1

const (
	DefaultBatchQueryLimit = 20
	MaxBatchQueryLimit     = 100
)

var (
	ErrNotFound        = errors.New("repository item not found")
	ErrAlreadyExists   = errors.New("repository item already exists")
	ErrVersionConflict = errors.New("repository version conflict")
	ErrLedgerCorrupt   = errors.New("ledger is corrupt")
)

type Snapshot struct {
	SchemaVersion int                                 `json:"schemaVersion"`
	Batches       map[string]domain.DeliveryBatch     `json:"batches"`
	Receipts      map[string]domain.ReceiptCredential `json:"receipts"`
}

type BatchQuery struct {
	Status      string
	RouteDate   string
	Origin      string
	Destination string
	Limit       int
	Offset      int
}

type BatchPage struct {
	Batches []domain.BatchSummary `json:"batches"`
	Total   int                   `json:"total"`
	Limit   int                   `json:"limit"`
	Offset  int                   `json:"offset"`
}

type Mutation struct {
	Changed       bool
	IgnoreVersion bool
	Result        any
}

type Store struct {
	mu   sync.RWMutex
	path string
	data Snapshot
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: 账本路径不能为空", ErrLedgerCorrupt)
	}
	store := &Store{path: path}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		store.data = emptySnapshot()
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: 读取账本失败: %v", ErrLedgerCorrupt, err)
	}
	if err := decodeSnapshot(content, &store.data); err != nil {
		return nil, err
	}
	return store, nil
}

func NewMemory() *Store {
	return &Store{data: emptySnapshot()}
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(s.data)
}

func (s *Store) GetBatch(ctx context.Context, id string) (domain.DeliveryBatch, error) {
	if err := contextError(ctx); err != nil {
		return domain.DeliveryBatch{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	batch, ok := s.data.Batches[id]
	if !ok {
		return domain.DeliveryBatch{}, fmt.Errorf("%w: batch %s", ErrNotFound, id)
	}
	return cloneBatch(batch)
}

func (s *Store) SearchBatches(ctx context.Context, query BatchQuery) (BatchPage, error) {
	if err := contextError(ctx); err != nil {
		return BatchPage{}, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = DefaultBatchQueryLimit
	}
	if limit < 1 || limit > MaxBatchQueryLimit || query.Offset < 0 {
		return BatchPage{}, fmt.Errorf("%w: 批次查询分页参数无效", domain.ErrInvalidData)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, err := cloneSnapshot(s.data)
	if err != nil {
		return BatchPage{}, err
	}
	if err := contextError(ctx); err != nil {
		return BatchPage{}, err
	}
	matched := make([]domain.DeliveryBatch, 0, len(snapshot.Batches))
	for _, batch := range snapshot.Batches {
		if query.Status != "" && batch.Status != query.Status {
			continue
		}
		if query.RouteDate != "" && batch.RouteDate != query.RouteDate {
			continue
		}
		if query.Origin != "" && batch.Origin != query.Origin {
			continue
		}
		if query.Destination != "" && batch.Destination != query.Destination {
			continue
		}
		matched = append(matched, batch)
	}
	sort.SliceStable(matched, func(left, right int) bool {
		if matched[left].RouteDate != matched[right].RouteDate {
			return matched[left].RouteDate < matched[right].RouteDate
		}
		if !matched[left].CreatedAt.Equal(matched[right].CreatedAt) {
			return matched[left].CreatedAt.Before(matched[right].CreatedAt)
		}
		return matched[left].ID < matched[right].ID
	})
	total := len(matched)
	start := query.Offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	results := make([]domain.BatchSummary, 0, end-start)
	for _, batch := range matched[start:end] {
		results = append(results, domain.SummarizeBatch(batch))
	}
	return BatchPage{Batches: results, Total: total, Limit: limit, Offset: query.Offset}, nil
}

func (s *Store) GetReceipt(ctx context.Context, id string) (domain.ReceiptCredential, error) {
	if err := contextError(ctx); err != nil {
		return domain.ReceiptCredential{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	receipt, ok := s.data.Receipts[id]
	if !ok {
		return domain.ReceiptCredential{}, fmt.Errorf("%w: receipt %s", ErrNotFound, id)
	}
	return cloneReceipt(receipt)
}

func (s *Store) CreateBatch(ctx context.Context, batch domain.DeliveryBatch) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := domain.ValidateBatch(batch); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return err
	}
	if _, exists := s.data.Batches[batch.ID]; exists {
		return fmt.Errorf("%w: batch %s", ErrAlreadyExists, batch.ID)
	}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return err
	}
	candidate.Batches[batch.ID] = batch
	if err := s.persistLocked(candidate); err != nil {
		return err
	}
	s.data = candidate
	return nil
}

func (s *Store) UpdateBatch(ctx context.Context, id string, expectedVersion int, mutate func(*domain.DeliveryBatch, *Snapshot) (Mutation, error)) (domain.DeliveryBatch, any, error) {
	if err := contextError(ctx); err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	if _, ok := s.data.Batches[id]; !ok {
		return domain.DeliveryBatch{}, nil, fmt.Errorf("%w: batch %s", ErrNotFound, id)
	}
	current := s.data.Batches[id]
	if expectedVersion != current.Version {
		return domain.DeliveryBatch{}, nil, fmt.Errorf("%w: expected %d, actual %d", ErrVersionConflict, expectedVersion, current.Version)
	}
	candidate, err := cloneSnapshot(s.data)
	if err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	batch := candidate.Batches[id]
	mutation, err := mutate(&batch, &candidate)
	if err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	if !mutation.Changed {
		current, cloneErr := cloneBatch(s.data.Batches[id])
		return current, mutation.Result, cloneErr
	}
	if err := contextError(ctx); err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	batch.Version = s.data.Batches[id].Version + 1
	if err := domain.ValidateBatch(batch); err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	candidate.Batches[id] = batch
	if err := validateSnapshot(candidate); err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	if err := s.persistLocked(candidate); err != nil {
		return domain.DeliveryBatch{}, nil, err
	}
	s.data = candidate
	result, err := cloneBatch(batch)
	return result, mutation.Result, err
}

func (s *Store) persistLocked(candidate Snapshot) error {
	if s.path == "" {
		return nil
	}
	if err := validateSnapshot(candidate); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("账本编码失败: %w", err)
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("创建账本目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(directory, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时账本失败: %w", err)
	}
	temporaryName := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("设置账本权限失败: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("写入临时账本失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步临时账本失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭临时账本失败: %w", err)
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("替换账本失败: %w", err)
	}
	keepTemporary = true
	return nil
}

func emptySnapshot() Snapshot {
	return Snapshot{SchemaVersion: currentSchemaVersion, Batches: map[string]domain.DeliveryBatch{}, Receipts: map[string]domain.ReceiptCredential{}}
}

func decodeSnapshot(content []byte, target *Snapshot) error {
	decoder := json.NewDecoder(bytesReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: JSON 格式无效: %v", ErrLedgerCorrupt, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: 账本包含尾随数据", ErrLedgerCorrupt)
	}
	if err := validateSnapshot(*target); err != nil {
		return fmt.Errorf("%w: %v", ErrLedgerCorrupt, err)
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf("schemaVersion 必须是 %d", currentSchemaVersion)
	}
	if snapshot.Batches == nil || snapshot.Receipts == nil {
		return errors.New("账本集合不能为空")
	}
	for id, batch := range snapshot.Batches {
		if id != batch.ID {
			return fmt.Errorf("批次索引与 ID 不一致: %s", id)
		}
		if err := domain.ValidateBatch(batch); err != nil {
			return err
		}
	}
	for id, receipt := range snapshot.Receipts {
		if id != receipt.ID {
			return fmt.Errorf("凭据索引与 ID 不一致: %s", id)
		}
		if err := domain.ValidateReceipt(receipt); err != nil {
			return err
		}
	}
	return nil
}

func cloneSnapshot(snapshot Snapshot) (Snapshot, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	var result Snapshot
	if err := json.Unmarshal(encoded, &result); err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func cloneBatch(batch domain.DeliveryBatch) (domain.DeliveryBatch, error) {
	encoded, err := json.Marshal(batch)
	if err != nil {
		return domain.DeliveryBatch{}, err
	}
	var result domain.DeliveryBatch
	if err := json.Unmarshal(encoded, &result); err != nil {
		return domain.DeliveryBatch{}, err
	}
	return result, nil
}

func cloneReceipt(receipt domain.ReceiptCredential) (domain.ReceiptCredential, error) {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return domain.ReceiptCredential{}, err
	}
	var result domain.ReceiptCredential
	if err := json.Unmarshal(encoded, &result); err != nil {
		return domain.ReceiptCredential{}, err
	}
	return result, nil
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

type byteReader struct {
	data []byte
	off  int
}

func bytesReader(data []byte) *byteReader {
	return &byteReader{data: data}
}

func (r *byteReader) Read(target []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(target, r.data[r.off:])
	r.off += n
	return n, nil
}
