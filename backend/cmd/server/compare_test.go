package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"dataflow-designer/internal/flow"
)

func doRequest(handler http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, path, reader)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestCandidateCompareEndpointsAndViewPersistence(t *testing.T) {
	directory := t.TempDir()
	storeFile := filepath.Join(directory, "store.json")
	store, err := newStore(storeFile, filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")

	encoded, _ := json.Marshal(publishTestProject())
	if response := doRequest(handler, http.MethodPost, "/api/projects", string(encoded)); response.Code != http.StatusOK {
		t.Fatalf("save status = %d", response.Code)
	}
	if response := doRequest(handler, http.MethodPost, "/api/projects/pub-http/run", "{}"); response.Code != http.StatusOK {
		t.Fatalf("run status = %d", response.Code)
	}
	// 修改来源后保存并重跑，产生第二条候选。
	project := publishTestProject()
	project.Nodes[0].Params["content"] = "id,city\n1,S\n3,C"
	encoded, _ = json.Marshal(project)
	if response := doRequest(handler, http.MethodPost, "/api/projects", string(encoded)); response.Code != http.StatusOK {
		t.Fatalf("resave status = %d", response.Code)
	}
	if response := doRequest(handler, http.MethodPost, "/api/projects/pub-http/run", "{}"); response.Code != http.StatusOK {
		t.Fatalf("rerun status = %d", response.Code)
	}

	// 筛选候选：按接收方。
	response := doRequest(handler, http.MethodGet, "/api/projects/pub-http/candidates?receiver=财务", "")
	if response.Code != http.StatusOK {
		t.Fatalf("candidates status = %d", response.Code)
	}
	var listed struct {
		Candidates []flow.CandidateView `json:"candidates"`
	}
	json.NewDecoder(response.Body).Decode(&listed)
	if len(listed.Candidates) != 2 {
		t.Fatalf("两次运行应归档 2 条候选: %#v", listed.Candidates)
	}
	// 按发布结果筛选：旧候选被取代，新候选待确认。
	response = doRequest(handler, http.MethodGet, "/api/projects/pub-http/candidates?status=pending", "")
	var pendingListed struct {
		Candidates []flow.CandidateView `json:"candidates"`
	}
	json.NewDecoder(response.Body).Decode(&pendingListed)
	if len(pendingListed.Candidates) != 1 {
		t.Fatalf("应只有 1 条待确认候选: %#v", pendingListed.Candidates)
	}

	// 并排对比：新增一行、删除一行。
	compareBody, _ := json.Marshal(map[string]string{"a": listed.Candidates[1].Key, "b": listed.Candidates[0].Key})
	response = doRequest(handler, http.MethodPost, "/api/projects/pub-http/compare", string(compareBody))
	if response.Code != http.StatusOK {
		t.Fatalf("compare status = %d", response.Code)
	}
	var comparison flow.Comparison
	json.NewDecoder(response.Body).Decode(&comparison)
	if comparison.Identical || len(comparison.Added) != 1 || len(comparison.Removed) != 1 {
		t.Fatalf("对比结果不符: %#v", comparison)
	}
	if len(comparison.BasisDiffs) == 0 {
		t.Fatal("不同运行的候选必须显示依据差异")
	}

	// 对比是只读的：接收方可见版本与失效资格不变。
	var projectState struct {
		Deliveries map[string]flow.DeliveryState `json:"deliveries"`
	}
	response = doRequest(handler, http.MethodGet, "/api/projects/pub-http", "")
	json.NewDecoder(response.Body).Decode(&projectState)
	if projectState.Deliveries["pub"].Published != nil {
		t.Fatal("对比操作不得改变接收方当前可见版本")
	}
	if projectState.Deliveries["pub"].Pending == nil || projectState.Deliveries["pub"].Pending.Stale {
		t.Fatal("待确认候选状态不应被对比操作改变")
	}

	// 保存筛选视图，重开工程（重建 store）后仍可继续核对。
	viewBody, _ := json.Marshal(flow.CompareView{Name: "财务待确认", Filter: flow.CandidateFilter{Receiver: "财务", Status: "pending"}, DiffOnly: true})
	if response := doRequest(handler, http.MethodPost, "/api/projects/pub-http/compare-views", string(viewBody)); response.Code != http.StatusOK {
		t.Fatalf("save view status = %d", response.Code)
	}
	reopened, err := newStore(storeFile, filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	reopenedHandler := buildMux(reopened, "")
	response = doRequest(reopenedHandler, http.MethodGet, "/api/projects/pub-http/compare-views", "")
	var views struct {
		Views []flow.CompareView `json:"views"`
	}
	json.NewDecoder(response.Body).Decode(&views)
	if len(views.Views) != 1 || views.Views[0].Name != "财务待确认" || !views.Views[0].DiffOnly {
		t.Fatalf("重开后视图应保留: %#v", views.Views)
	}
	// 重开后候选档案仍在，可继续对比。
	response = doRequest(reopenedHandler, http.MethodGet, "/api/projects/pub-http/candidates", "")
	var reloaded struct {
		Candidates []flow.CandidateView `json:"candidates"`
	}
	json.NewDecoder(response.Body).Decode(&reloaded)
	if len(reloaded.Candidates) != 2 {
		t.Fatalf("重开后候选应保留: %#v", reloaded.Candidates)
	}
	// 删除视图。
	if response := doRequest(reopenedHandler, http.MethodDelete, "/api/projects/pub-http/compare-views/财务待确认", ""); response.Code != http.StatusOK {
		t.Fatalf("delete view status = %d", response.Code)
	}
}
