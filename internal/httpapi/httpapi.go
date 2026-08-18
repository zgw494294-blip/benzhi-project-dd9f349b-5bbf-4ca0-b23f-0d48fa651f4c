package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coldchain-route-ledger/internal/domain"
	"coldchain-route-ledger/internal/service"
)

const maxRequestBody = 1 << 20

// webFiles keeps the operator workspace available from the same binary as the API.
//
//go:embed web/*
var webFiles embed.FS

var webHandler = func() http.Handler {
	root, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(root))
}()

type Server struct {
	service *service.Service
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type createRequest struct {
	RouteDate   string       `json:"routeDate"`
	Origin      string       `json:"origin"`
	Destination string       `json:"destination"`
	Boxes       []boxRequest `json:"boxes"`
}

type boxRequest struct {
	ID                 string  `json:"id"`
	Label              string  `json:"label"`
	DrugName           string  `json:"drugName"`
	Quantity           int     `json:"quantity"`
	RequiredMinCelsius float64 `json:"requiredMinCelsius"`
	RequiredMaxCelsius float64 `json:"requiredMaxCelsius"`
}

type dispatchRequest struct {
	ExpectedVersion int           `json:"expectedVersion"`
	Seals           []sealRequest `json:"seals"`
}

type sealRequest struct {
	BoxID    string `json:"boxID"`
	SealCode string `json:"sealCode"`
}

type handoffRequest struct {
	ExpectedVersion    int     `json:"expectedVersion"`
	FromParty          string  `json:"fromParty"`
	ToParty            string  `json:"toParty"`
	OccurredAt         string  `json:"occurredAt"`
	Location           string  `json:"location"`
	TemperatureCelsius float64 `json:"temperatureCelsius"`
	Unit               string  `json:"unit"`
	Notes              string  `json:"notes"`
	IdempotencyKey     string  `json:"idempotencyKey"`
}

type receiveRequest struct {
	ExpectedVersion int    `json:"expectedVersion"`
	BoxID           string `json:"boxID"`
	Quantity        int    `json:"quantity"`
	Condition       string `json:"condition"`
	ExceptionNote   string `json:"exceptionNote"`
}

type receiveBatchRequest struct {
	ExpectedVersion int                `json:"expectedVersion"`
	Items           []receiveBatchItem `json:"items"`
}

type receiveBatchItem struct {
	BoxID         string `json:"boxID"`
	Quantity      int    `json:"quantity"`
	Condition     string `json:"condition"`
	ExceptionNote string `json:"exceptionNote"`
}

type closeRequest struct {
	ExpectedVersion int    `json:"expectedVersion"`
	Receiver        string `json:"receiver"`
}

func NewServer(svc *service.Service) *Server {
	return &Server{service: svc}
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := contextError(request.Context()); err != nil {
		writeError(writer, err)
		return
	}
	path := strings.Trim(request.URL.Path, "/")
	if path == "" || !strings.HasPrefix(path, "v1/") {
		s.serveWeb(writer, request)
		return
	}
	if path == "v1/batches" {
		switch request.Method {
		case http.MethodGet:
			s.searchBatches(writer, request)
		case http.MethodPost:
			s.createBatch(writer, request)
		default:
			methodError(writer, "GET, POST")
		}
		return
	}
	if path == "v1/selfcheck" {
		if request.Method != http.MethodGet {
			methodError(writer, http.MethodGet)
			return
		}
		if err := service.RunSelfCheck(request.Context()); err != nil {
			writeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "batches" || parts[2] == "" || strings.Contains(parts[2], "?") {
		writeErrorStatus(writer, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	id := parts[2]
	if len(parts) == 3 {
		if request.Method != http.MethodGet {
			methodError(writer, http.MethodGet)
			return
		}
		batch, err := s.service.GetBatch(request.Context(), id)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]domain.DeliveryBatch{"batch": batch})
		return
	}
	if len(parts) != 4 {
		writeErrorStatus(writer, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	switch parts[3] {
	case "dispatch":
		if request.Method != http.MethodPost {
			methodError(writer, http.MethodPost)
			return
		}
		s.dispatch(writer, request, id)
	case "handoffs":
		if request.Method != http.MethodPost {
			methodError(writer, http.MethodPost)
			return
		}
		s.handoff(writer, request, id)
	case "receive":
		if request.Method != http.MethodPost {
			methodError(writer, http.MethodPost)
			return
		}
		s.receive(writer, request, id)
	case "receive-batch":
		if request.Method != http.MethodPost {
			methodError(writer, http.MethodPost)
			return
		}
		s.receiveBatch(writer, request, id)
	case "close":
		if request.Method != http.MethodPost {
			methodError(writer, http.MethodPost)
			return
		}
		s.close(writer, request, id)
	case "events":
		if request.Method != http.MethodGet {
			methodError(writer, http.MethodGet)
			return
		}
		s.events(writer, request, id)
	case "receipt":
		if request.Method != http.MethodGet {
			methodError(writer, http.MethodGet)
			return
		}
		s.receipt(writer, request, id)
	default:
		writeErrorStatus(writer, http.StatusNotFound, "not_found", "资源不存在")
	}
}

func (s *Server) serveWeb(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodError(writer, "GET, HEAD")
		return
	}
	writer.Header().Set("Cache-Control", "no-cache")
	webHandler.ServeHTTP(writer, request)
}

func (s *Server) searchBatches(writer http.ResponseWriter, request *http.Request) {
	query, ok := parseBatchSearchRequest(writer, request.URL.RawQuery)
	if !ok {
		return
	}
	page, err := s.service.SearchBatches(request.Context(), query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (s *Server) createBatch(writer http.ResponseWriter, request *http.Request) {
	var payload createRequest
	if err := decodeJSON(writer, request, &payload); err != nil {
		return
	}
	boxes := make([]domain.NewBoxInput, 0, len(payload.Boxes))
	for _, box := range payload.Boxes {
		boxes = append(boxes, domain.NewBoxInput{ID: box.ID, Label: box.Label, DrugName: box.DrugName, Quantity: box.Quantity, RequiredMinCelsius: box.RequiredMinCelsius, RequiredMaxCelsius: box.RequiredMaxCelsius})
	}
	batch, err := s.service.CreateBatch(request.Context(), service.CreateBatchRequest{RouteDate: payload.RouteDate, Origin: payload.Origin, Destination: payload.Destination, Boxes: boxes})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]domain.DeliveryBatch{"batch": batch})
}

func (s *Server) dispatch(writer http.ResponseWriter, request *http.Request, id string) {
	var payload dispatchRequest
	if err := decodeJSON(writer, request, &payload); err != nil {
		return
	}
	seals := make(map[string]string, len(payload.Seals))
	for _, seal := range payload.Seals {
		if _, exists := seals[seal.BoxID]; exists {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "封签列表不能包含重复药箱")
			return
		}
		seals[seal.BoxID] = seal.SealCode
	}
	batch, err := s.service.Dispatch(request.Context(), id, service.DispatchRequest{ExpectedVersion: payload.ExpectedVersion, Seals: seals})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]domain.DeliveryBatch{"batch": batch})
}

func (s *Server) handoff(writer http.ResponseWriter, request *http.Request, id string) {
	var payload handoffRequest
	if err := decodeJSON(writer, request, &payload); err != nil {
		return
	}
	var occurredAt time.Time
	if payload.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, payload.OccurredAt)
		if err != nil {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "occurredAt 必须是 RFC3339 时间")
			return
		}
		occurredAt = parsed
	}
	result, err := s.service.Handoff(request.Context(), id, service.HandoffRequest{ExpectedVersion: payload.ExpectedVersion, FromParty: payload.FromParty, ToParty: payload.ToParty, OccurredAt: occurredAt, Location: payload.Location, TemperatureCelsius: payload.TemperatureCelsius, Unit: payload.Unit, Notes: payload.Notes, IdempotencyKey: payload.IdempotencyKey})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) receive(writer http.ResponseWriter, request *http.Request, id string) {
	var payload receiveRequest
	if err := decodeJSON(writer, request, &payload); err != nil {
		return
	}
	batch, err := s.service.Receive(request.Context(), id, service.ReceiveRequest{ExpectedVersion: payload.ExpectedVersion, BoxID: payload.BoxID, Quantity: payload.Quantity, Condition: payload.Condition, ExceptionNote: payload.ExceptionNote})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]domain.DeliveryBatch{"batch": batch})
}

func (s *Server) receiveBatch(writer http.ResponseWriter, request *http.Request, id string) {
	var payload receiveBatchRequest
	if err := decodeJSON(writer, request, &payload); err != nil {
		return
	}
	items := make([]domain.ReceiveInput, 0, len(payload.Items))
	for _, item := range payload.Items {
		items = append(items, domain.ReceiveInput{BoxID: item.BoxID, Quantity: item.Quantity, Condition: item.Condition, ExceptionNote: item.ExceptionNote})
	}
	batch, err := s.service.ReceiveBatch(request.Context(), id, service.ReceiveBatchRequest{ExpectedVersion: payload.ExpectedVersion, Items: items})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]domain.DeliveryBatch{"batch": batch})
}

func (s *Server) close(writer http.ResponseWriter, request *http.Request, id string) {
	var payload closeRequest
	if err := decodeJSON(writer, request, &payload); err != nil {
		return
	}
	result, err := s.service.Close(request.Context(), id, service.CloseRequest{ExpectedVersion: payload.ExpectedVersion, Receiver: payload.Receiver})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) events(writer http.ResponseWriter, request *http.Request, id string) {
	limit := 20
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeErrorStatus(writer, http.StatusBadRequest, "invalid_cursor", "limit 必须是整数")
			return
		}
		limit = parsed
	}
	page, err := s.service.Events(request.Context(), id, request.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (s *Server) receipt(writer http.ResponseWriter, request *http.Request, id string) {
	receipt, err := s.service.Receipt(request.Context(), id)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]domain.ReceiptCredential{"receipt": receipt})
}

func parseBatchSearchRequest(writer http.ResponseWriter, rawQuery string) (service.BatchSearchRequest, bool) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "查询参数编码无效")
		return service.BatchSearchRequest{}, false
	}
	allowed := map[string]struct{}{"status": {}, "routeDate": {}, "origin": {}, "destination": {}, "limit": {}, "offset": {}}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "查询参数不受支持: "+key)
			return service.BatchSearchRequest{}, false
		}
	}
	read := func(key string) (string, bool) {
		items, exists := values[key]
		if !exists {
			return "", true
		}
		if len(items) != 1 || strings.TrimSpace(items[0]) == "" {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", key+" 必须只提供一个非空值")
			return "", false
		}
		return items[0], true
	}
	status, ok := read("status")
	if !ok {
		return service.BatchSearchRequest{}, false
	}
	routeDate, ok := read("routeDate")
	if !ok {
		return service.BatchSearchRequest{}, false
	}
	origin, ok := read("origin")
	if !ok {
		return service.BatchSearchRequest{}, false
	}
	destination, ok := read("destination")
	if !ok {
		return service.BatchSearchRequest{}, false
	}
	limitValue, ok := read("limit")
	if !ok {
		return service.BatchSearchRequest{}, false
	}
	offsetValue, ok := read("offset")
	if !ok {
		return service.BatchSearchRequest{}, false
	}
	request := service.BatchSearchRequest{Status: status, RouteDate: routeDate, Origin: origin, Destination: destination}
	if limitValue != "" {
		request.Limit, err = strconv.Atoi(limitValue)
		if err != nil {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "limit 必须是整数")
			return service.BatchSearchRequest{}, false
		}
		if request.Limit < 1 || request.Limit > 100 {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "limit 必须在 1 到 100 之间")
			return service.BatchSearchRequest{}, false
		}
	}
	if offsetValue != "" {
		request.Offset, err = strconv.Atoi(offsetValue)
		if err != nil {
			writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "offset 必须是整数")
			return service.BatchSearchRequest{}, false
		}
	}
	return request, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		message := "请求体 JSON 无效"
		if errors.Is(err, http.ErrBodyReadAfterClose) {
			message = "请求体读取失败"
		}
		writeErrorStatus(writer, http.StatusBadRequest, "validation_error", message)
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeErrorStatus(writer, http.StatusBadRequest, "validation_error", "请求体只能包含一个 JSON 值")
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	if err := contextError(request.Context()); err != nil {
		writeError(writer, err)
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	if writer.Header().Get("Content-Type") == "" {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}

func writeError(writer http.ResponseWriter, err error) {
	status, code, message := classifyError(err)
	writeErrorStatus(writer, status, code, message)
}

func writeErrorStatus(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

func classifyError(err error) (int, string, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return 499, "request_canceled", "请求已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "request_timeout", "请求已超时"
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound, "not_found", "批次或凭据不存在"
	case errors.Is(err, service.ErrConflict), errors.Is(err, service.ErrIdempotency):
		return http.StatusConflict, "conflict", "批次版本或幂等请求冲突"
	case errors.Is(err, service.ErrValidation), errors.Is(err, domain.ErrInvalidData), errors.Is(err, domain.ErrInvalidState):
		return http.StatusBadRequest, "validation_error", "请求不符合业务规则"
	default:
		return http.StatusInternalServerError, "internal_error", "服务暂时无法完成请求"
	}
}

func methodError(writer http.ResponseWriter, method string) {
	writer.Header().Set("Allow", method)
	writeErrorStatus(writer, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不受支持")
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
