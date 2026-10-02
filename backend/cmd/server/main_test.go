package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dataflow-designer/internal/flow"
)

func sampleProject() flow.Project {
	return flow.Project{
		Format: "dataflow.project/v1", ID: "http-test", Name: "HTTP test",
		Nodes: []flow.Node{
			{ID: "in", Type: "file_input", Name: "输入", Params: map[string]any{"fileName": "a.csv", "format": "csv", "content": "id,city\n1,S"}},
			{ID: "out", Type: "file_output", Name: "输出", Params: map[string]any{"path": "out.json", "format": "json"}},
		},
		Edges: []flow.Edge{{ID: "io", Source: "in", Target: "out", SourcePort: "rows", TargetPort: "rows"}},
	}
}

func TestSaveRunAndReloadProject(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(sampleProject())
	handler := buildMux(store, "")
	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(encoded))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d", response.Code)
	}
	var saved struct {
		Project flow.Project `json:"project"`
	}
	json.NewDecoder(response.Body).Decode(&saved)
	if saved.Project.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Project.Revision)
	}

	runRequest := httptest.NewRequest(http.MethodPost, "/api/projects/http-test/run", bytes.NewBufferString(`{"startNodeIds":[]}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, runRequest)
	if response.Code != http.StatusOK {
		t.Fatalf("run status = %d", response.Code)
	}
	var run flow.RunResult
	json.NewDecoder(response.Body).Decode(&run)
	if !run.OK || run.Results["out"].Artifact == nil {
		t.Fatalf("run should produce output: %#v", run)
	}

	reloadedStore, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedStore.artifacts["http-test"]) != 2 {
		t.Fatalf("artifacts after reload = %d, want 2", len(reloadedStore.artifacts["http-test"]))
	}
}

func TestInvalidProjectDoesNotRun(t *testing.T) {
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	project := sampleProject()
	project.Edges = append(project.Edges, flow.Edge{ID: "cycle", Source: "out", Target: "in", SourcePort: "rows", TargetPort: "rows"})
	encoded, _ := json.Marshal(project)
	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(encoded))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save status = %d, want 422", response.Code)
	}
	if _, exists := store.projects[project.ID]; exists {
		t.Fatal("invalid project must not replace executable revision")
	}
}

func TestStaticSpaFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, dir)
	for _, path := range []string{"/", "/some/spa/route"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "app</html>") {
			t.Fatalf("GET %s = %d, want index.html", path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("api must not be shadowed by static handler: %d", response.Code)
	}
}

func publishTestProject() flow.Project {
	return flow.Project{
		Format: "dataflow.project/v1", ID: "pub-http", Name: "发布测试",
		Nodes: []flow.Node{
			{ID: "in", Type: "file_input", Name: "输入", Params: map[string]any{"fileName": "a.csv", "format": "csv", "content": "id,city\n1,S\n2,B"}},
			{ID: "pub", Type: "publish", Name: "发布", Params: map[string]any{"receiver": "财务"}},
		},
		Edges: []flow.Edge{{ID: "ip", Source: "in", Target: "pub", SourcePort: "rows", TargetPort: "rows"}},
	}
}

func TestEmptyProjectSaveReturnsEmptyCollections(t *testing.T) {
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(`{"format":"dataflow.project/v1","id":"blank","name":"空","revision":0,"nodes":null,"edges":null}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("empty save status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"nodes":[]`) || !strings.Contains(body, `"edges":[]`) {
		t.Fatalf("empty project must serialize empty collections, got %s", body)
	}
	// 重新打开同样必须是 []，前端拿到后可以继续编辑而不是整页崩溃。
	request = httptest.NewRequest(http.MethodGet, "/api/projects/blank", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `"nodes":[]`) {
		t.Fatalf("reopened project must keep empty node collection, got %s", response.Body.String())
	}
}

func TestRunConfirmAndReloadDeliveries(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	encoded, _ := json.Marshal(publishTestProject())
	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(encoded))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/pub-http/run", bytes.NewBufferString(`{"startNodeIds":[]}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var run flow.RunResult
	json.NewDecoder(response.Body).Decode(&run)
	if !run.OK || run.Deliveries["pub"].Pending == nil {
		t.Fatalf("run should compute a pending delivery: %#v", run)
	}

	// 确认途中重新打开：接收方视角应与确认前一致（仍不可见）。
	request = httptest.NewRequest(http.MethodGet, "/api/projects/pub-http", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var reopened struct {
		Deliveries map[string]flow.DeliveryState `json:"deliveries"`
	}
	json.NewDecoder(response.Body).Decode(&reopened)
	if reopened.Deliveries["pub"].Pending == nil || reopened.Deliveries["pub"].Published != nil {
		t.Fatalf("确认途中重开应看到仅已计算状态: %#v", reopened.Deliveries["pub"])
	}

	request = httptest.NewRequest(http.MethodPost, "/api/projects/pub-http/confirm", bytes.NewBufferString(`{"all":true}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body %s", response.Code, response.Body.String())
	}

	// 重启（重新加载磁盘）后，已发布证据必须保留。
	reloaded, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	state := reloaded.deliveries["pub-http"]["pub"]
	if state.Published == nil || state.Published.RunID == "" {
		t.Fatalf("published delivery must survive reload: %#v", state)
	}
	if len(state.History) == 0 {
		t.Fatal("delivery evidence history must survive reload")
	}
}
