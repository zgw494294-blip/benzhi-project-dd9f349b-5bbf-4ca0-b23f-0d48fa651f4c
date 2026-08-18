package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"coldchain-route-ledger/internal/repository"
	"coldchain-route-ledger/internal/service"
)

func TestHTTPAPIRejectsUnknownAndTrailingJSON(t *testing.T) {
	handler := NewServer(service.New(repository.NewMemory()))
	valid := `{"routeDate":"2026-08-19","origin":"A","destination":"B","boxes":[{"id":"box","label":"A","drugName":"药品","quantity":1,"requiredMinCelsius":2,"requiredMaxCelsius":8}]}`
	for name, body := range map[string]string{"unknown": strings.TrimSuffix(valid, "}") + `,"extra":true}`, "trailing": valid + valid} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(body))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，响应 = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHTTPAPIServesBrowserWorkspace(t *testing.T) {
	handler := NewServer(service.New(repository.NewMemory()))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("首页状态码 = %d，响应 = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("首页 Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
	if !strings.Contains(recorder.Body.String(), "冷链路线账本") {
		t.Fatalf("首页不是可用浏览器页面: %s", recorder.Body.String())
	}
}

func TestHTTPAPIRunsFullWorkflowAndMapsQueries(t *testing.T) {
	handler := NewServer(service.New(repository.NewMemory()))
	create := doJSON(t, handler, http.MethodPost, "/v1/batches", `{"routeDate":"2026-08-19","origin":"社区药房","destination":"卫生站","boxes":[{"id":"box-a","label":"A","drugName":"疫苗","quantity":2,"requiredMinCelsius":2,"requiredMaxCelsius":8},{"id":"box-b","label":"B","drugName":"胰岛素","quantity":1,"requiredMinCelsius":2,"requiredMaxCelsius":8}]}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("创建状态码 = %d，响应 = %s", create.Code, create.Body.String())
	}
	var created struct {
		Batch struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"batch"`
	}
	decodeResponse(t, create, &created)
	id := created.Batch.ID
	dispatch := doJSON(t, handler, http.MethodPost, "/v1/batches/"+id+"/dispatch", `{"expectedVersion":1,"seals":[{"boxID":"box-a","sealCode":"a"},{"boxID":"box-b","sealCode":"b"}]}`)
	if dispatch.Code != http.StatusOK {
		t.Fatalf("发运状态码 = %d，响应 = %s", dispatch.Code, dispatch.Body.String())
	}
	handoffBody := `{"expectedVersion":2,"fromParty":"社区药房","toParty":"配送员","location":"冷库","temperatureCelsius":5,"unit":"C","idempotencyKey":"http-1"}`
	handoff := doJSON(t, handler, http.MethodPost, "/v1/batches/"+id+"/handoffs", handoffBody)
	if handoff.Code != http.StatusOK {
		t.Fatalf("交接状态码 = %d，响应 = %s", handoff.Code, handoff.Body.String())
	}
	if replay := doJSON(t, handler, http.MethodPost, "/v1/batches/"+id+"/handoffs", handoffBody); replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("交接重放响应错误: %d %s", replay.Code, replay.Body.String())
	}
	receiveA := doJSON(t, handler, http.MethodPost, "/v1/batches/"+id+"/receive", `{"expectedVersion":3,"boxID":"box-a","quantity":2,"condition":"accepted"}`)
	if receiveA.Code != http.StatusOK {
		t.Fatalf("首箱签收状态码 = %d，响应 = %s", receiveA.Code, receiveA.Body.String())
	}
	receiveB := doJSON(t, handler, http.MethodPost, "/v1/batches/"+id+"/receive", `{"expectedVersion":4,"boxID":"box-b","quantity":1,"condition":"accepted"}`)
	if receiveB.Code != http.StatusOK {
		t.Fatalf("次箱签收状态码 = %d，响应 = %s", receiveB.Code, receiveB.Body.String())
	}
	closeResponse := doJSON(t, handler, http.MethodPost, "/v1/batches/"+id+"/close", `{"expectedVersion":5,"receiver":"卫生站收货员"}`)
	if closeResponse.Code != http.StatusOK || !strings.Contains(closeResponse.Body.String(), `"receiptHash"`) {
		t.Fatalf("关闭响应错误: %d %s", closeResponse.Code, closeResponse.Body.String())
	}
	events := doJSON(t, handler, http.MethodGet, "/v1/batches/"+id+"/events?limit=2", "")
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"nextCursor"`) {
		t.Fatalf("事件响应错误: %d %s", events.Code, events.Body.String())
	}
	invalidCursor := doJSON(t, handler, http.MethodGet, "/v1/batches/"+id+"/events?cursor=invalid", "")
	if invalidCursor.Code != http.StatusBadRequest {
		t.Fatalf("非法游标状态码 = %d，响应 = %s", invalidCursor.Code, invalidCursor.Body.String())
	}
	receipt := doJSON(t, handler, http.MethodGet, "/v1/batches/"+id+"/receipt", "")
	if receipt.Code != http.StatusOK || !strings.Contains(receipt.Body.String(), `"receiptHash"`) {
		t.Fatalf("凭据响应错误: %d %s", receipt.Code, receipt.Body.String())
	}
}

func TestHTTPAPISearchesAndReceivesBatch(t *testing.T) {
	handler := NewServer(service.New(repository.NewMemory()))
	create := func(routeDate string, boxes string) string {
		t.Helper()
		response := doJSON(t, handler, http.MethodPost, "/v1/batches", `{"routeDate":"`+routeDate+`","origin":"社区药房","destination":"卫生站","boxes":`+boxes+`}`)
		if response.Code != http.StatusCreated {
			t.Fatalf("创建状态码 = %d，响应 = %s", response.Code, response.Body.String())
		}
		var created struct {
			Batch struct {
				ID string `json:"id"`
			} `json:"batch"`
		}
		decodeResponse(t, response, &created)
		return created.Batch.ID
	}
	firstID := create("2026-08-19", `[{"id":"box-a","label":"A","drugName":"疫苗","quantity":2,"requiredMinCelsius":2,"requiredMaxCelsius":8},{"id":"box-b","label":"B","drugName":"胰岛素","quantity":3,"requiredMinCelsius":2,"requiredMaxCelsius":8}]`)
	create("2026-08-20", `[{"id":"box-c","label":"C","drugName":"疫苗","quantity":1,"requiredMinCelsius":2,"requiredMaxCelsius":8}]`)
	create("2026-08-18", `[{"id":"box-d","label":"D","drugName":"疫苗","quantity":1,"requiredMinCelsius":2,"requiredMaxCelsius":8}]`)
	if response := doJSON(t, handler, http.MethodPost, "/v1/batches/"+firstID+"/dispatch", `{"expectedVersion":1,"seals":[{"boxID":"box-a","sealCode":"seal-a"},{"boxID":"box-b","sealCode":"seal-b"}]}`); response.Code != http.StatusOK {
		t.Fatalf("发运状态码 = %d，响应 = %s", response.Code, response.Body.String())
	}
	if response := doJSON(t, handler, http.MethodPost, "/v1/batches/"+firstID+"/handoffs", `{"expectedVersion":2,"fromParty":"社区药房","toParty":"配送员","location":"冷库","temperatureCelsius":5,"unit":"C","idempotencyKey":"batch-search-1"}`); response.Code != http.StatusOK {
		t.Fatalf("交接状态码 = %d，响应 = %s", response.Code, response.Body.String())
	}
	invalid := `{"expectedVersion":3,"items":[{"boxID":"box-a","quantity":2,"condition":"accepted"},{"boxID":"unknown","quantity":1,"condition":"accepted"}]}`
	if response := doJSON(t, handler, http.MethodPost, "/v1/batches/"+firstID+"/receive-batch", invalid); response.Code != http.StatusBadRequest {
		t.Fatalf("失败批量验收状态码 = %d，响应 = %s", response.Code, response.Body.String())
	}
	var unchanged struct {
		Batch struct {
			Version int    `json:"version"`
			Status  string `json:"status"`
			Boxes   []struct {
				AcceptedAt time.Time `json:"acceptedAt"`
			} `json:"boxes"`
		} `json:"batch"`
	}
	getAfterFailure := doJSON(t, handler, http.MethodGet, "/v1/batches/"+firstID, "")
	decodeResponse(t, getAfterFailure, &unchanged)
	if unchanged.Batch.Version != 3 || unchanged.Batch.Status != "Dispatched" || len(unchanged.Batch.Boxes) != 2 || !unchanged.Batch.Boxes[0].AcceptedAt.IsZero() {
		t.Fatalf("失败批量验收产生了部分写入: %s", getAfterFailure.Body.String())
	}
	valid := `{"expectedVersion":3,"items":[{"boxID":"box-a","quantity":2,"condition":"accepted"},{"boxID":"box-b","quantity":3,"condition":"accepted"}]}`
	if response := doJSON(t, handler, http.MethodPost, "/v1/batches/"+firstID+"/receive-batch", valid); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":4`) {
		t.Fatalf("批量验收响应错误: %d %s", response.Code, response.Body.String())
	}
	var page struct {
		Batches []struct {
			ID            string `json:"id"`
			Version       int    `json:"version"`
			TotalBoxes    int    `json:"totalBoxes"`
			ReceivedBoxes int    `json:"receivedBoxes"`
		} `json:"batches"`
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	search := doJSON(t, handler, http.MethodGet, "/v1/batches?status=Received&routeDate=2026-08-19", "")
	if search.Code != http.StatusOK {
		t.Fatalf("筛选状态码 = %d，响应 = %s", search.Code, search.Body.String())
	}
	decodeResponse(t, search, &page)
	if page.Total != 1 || len(page.Batches) != 1 || page.Batches[0].ID != firstID || page.Batches[0].Version != 4 || page.Batches[0].TotalBoxes != 2 || page.Batches[0].ReceivedBoxes != 2 || page.Limit != 20 || page.Offset != 0 {
		t.Fatalf("筛选结果错误: %+v", page)
	}
	paged := doJSON(t, handler, http.MethodGet, "/v1/batches?limit=2&offset=1", "")
	if paged.Code != http.StatusOK {
		t.Fatalf("分页状态码 = %d，响应 = %s", paged.Code, paged.Body.String())
	}
	decodeResponse(t, paged, &page)
	if page.Total != 3 || len(page.Batches) != 2 || page.Batches[0].ID == "" || page.Batches[1].ID == "" || page.Limit != 2 || page.Offset != 1 {
		t.Fatalf("分页结果错误: %+v", page)
	}
	for _, query := range []string{"status=Unknown", "routeDate=2026/08/19", "offset=-1", "limit=0", "limit=101", "unsupported=x"} {
		if response := doJSON(t, handler, http.MethodGet, "/v1/batches?"+query, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("非法查询 %s 状态码 = %d，响应 = %s", query, response.Code, response.Body.String())
		}
	}
}

func TestHTTPAPIRejectsOversizedBody(t *testing.T) {
	handler := NewServer(service.New(repository.NewMemory()))
	body := `{"routeDate":"2026-08-19","origin":"A","destination":"B","boxes":[]}` + strings.Repeat(" ", maxRequestBody)
	response := doJSON(t, handler, http.MethodPost, "/v1/batches", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("超大请求状态码 = %d，响应 = %s", response.Code, response.Body.String())
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("响应 JSON 无效: %v; body=%s", err, response.Body.String())
	}
}
