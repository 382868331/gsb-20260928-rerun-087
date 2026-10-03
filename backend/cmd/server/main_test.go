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

func postJSON(handler http.Handler, path string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func saveProject(t *testing.T, handler http.Handler, project flow.Project) {
	t.Helper()
	encoded, _ := json.Marshal(project)
	response := postJSON(handler, "/api/projects", string(encoded))
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d, body %s", response.Code, response.Body.String())
	}
}

func runProject(t *testing.T, handler http.Handler, id string) flow.RunResult {
	t.Helper()
	response := postJSON(handler, "/api/projects/"+id+"/run", `{"startNodeIds":[]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("run status = %d, body %s", response.Code, response.Body.String())
	}
	var run flow.RunResult
	json.NewDecoder(response.Body).Decode(&run)
	return run
}

func confirmProject(handler http.Handler, id string, body string) *httptest.ResponseRecorder {
	return postJSON(handler, "/api/projects/"+id+"/confirm", body)
}

// 双窗口工作流 + 保存重开检查：
// 候选计算后修改工程 → 确认被拒并持久失效 → 恢复原文也不能恢复资格 →
// 重跑产生新候选 → 旧页面迟到的确认不能误发布 → 重复请求幂等 → 重开后证据保留。
func TestTwoWindowConfirmRevertAndReopen(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	project := publishTestProject()
	originalContent := project.Nodes[0].Params["content"]
	saveProject(t, handler, project)

	// 窗口 A：运行后看到候选 v1（仅已计算）。
	first := runProject(t, handler, "pub-http")
	seen := first.Deliveries["pub"].Pending
	if seen == nil {
		t.Fatal("run should compute a pending candidate")
	}

	// 窗口 B：修改来源并保存——候选立即失效，且失效状态持久化。
	project.Nodes[0].Params["content"] = "id,city\n9,Z"
	saveProject(t, handler, project)
	if pending := store.deliveries["pub-http"]["pub"].Pending; pending == nil || !pending.Stale {
		t.Fatalf("保存后候选应立即失效: %#v", pending)
	}

	// 窗口 B：把来源恢复原文（不重跑）——失效不能复活。
	project.Nodes[0].Params["content"] = originalContent
	saveProject(t, handler, project)
	response := confirmProject(handler, "pub-http", `{"all":true}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("恢复原文不能恢复发布资格: %d %s", response.Code, response.Body.String())
	}
	// 保存重开后失效状态仍在：不是一次请求里的临时判断。
	reloaded, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	reopened := buildMux(reloaded, "")
	if response := confirmProject(reopened, "pub-http", `{"all":true}`); response.Code != http.StatusConflict {
		t.Fatalf("重开后失效候选仍必须拒绝确认: %d", response.Code)
	}
	state := reloaded.deliveries["pub-http"]["pub"]
	if state.Pending == nil || !state.Pending.Stale || state.Published != nil {
		t.Fatalf("失效候选只能留作比较: %#v", state)
	}
	foundInvalidated, foundRejected := false, false
	for _, event := range state.History {
		if event.Kind == flow.EventInvalidated {
			foundInvalidated = true
		}
		if event.Kind == flow.EventRejected {
			foundRejected = true
		}
	}
	if !foundInvalidated || !foundRejected {
		t.Fatalf("失效与拒绝都必须可追溯: %#v", state.History)
	}

	// 窗口 B：重跑产生全新候选（内容变化，版本不同）。
	project.Nodes[0].Params["content"] = "id,city\n1,S\n2,B\n3,C"
	saveProject(t, handler, project)
	second := runProject(t, handler, "pub-http")
	fresh := second.Deliveries["pub"].Pending
	if fresh == nil || fresh.Stale || fresh.Version == seen.Version {
		t.Fatalf("重跑应产生全新候选: %#v", fresh)
	}

	// 窗口 A 迟到的确认（绑定旧版本/旧批次）：必须拒绝，不能误发布新候选。
	lateBody, _ := json.Marshal(map[string]any{
		"all":      true,
		"expected": []map[string]string{{"nodeId": "pub", "version": seen.Version, "runId": seen.RunID}},
	})
	response = confirmProject(handler, "pub-http", string(lateBody))
	if response.Code != http.StatusConflict {
		t.Fatalf("迟到的旧确认必须被拒绝: %d %s", response.Code, response.Body.String())
	}
	if store.deliveries["pub-http"]["pub"].Published != nil {
		t.Fatal("迟到的旧确认不能误发布新结果")
	}

	// 窗口 B 确认自己看过的候选：成功发布。
	freshBody, _ := json.Marshal(map[string]any{
		"all":      true,
		"expected": []map[string]string{{"nodeId": "pub", "version": fresh.Version, "runId": fresh.RunID}},
	})
	response = confirmProject(handler, "pub-http", string(freshBody))
	if response.Code != http.StatusOK {
		t.Fatalf("看过当前候选的确认应成功: %d %s", response.Code, response.Body.String())
	}
	// 重复请求（网络重试）：幂等成功，不重复发布。
	if response := confirmProject(handler, "pub-http", string(freshBody)); response.Code != http.StatusOK {
		t.Fatalf("重复确认应幂等成功: %d %s", response.Code, response.Body.String())
	}

	// 重开后：已发布内容保留，每次拒绝/重算/投递都可追溯，发布证据不重复。
	reloaded, err = newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	final := reloaded.deliveries["pub-http"]["pub"]
	if final.Published == nil || final.Published.Version != fresh.Version {
		t.Fatalf("已发布内容必须保留: %#v", final)
	}
	publishedEvents, computedEvents := 0, 0
	for _, event := range final.History {
		switch event.Kind {
		case flow.EventPublished:
			publishedEvents++
		case flow.EventComputed:
			computedEvents++
		}
	}
	if publishedEvents != 1 || computedEvents < 2 {
		t.Fatalf("拒绝/重算/投递都必须可追溯且不重复: %#v", final.History)
	}
}
