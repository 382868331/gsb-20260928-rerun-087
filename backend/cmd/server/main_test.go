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
	handler := buildMux(store)
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
	handler := buildMux(store)
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

func TestLatestRunPersistedAndVersionAlignsOnReopen(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store)
	encoded, _ := json.Marshal(sampleProject())
	serve := func(method string, path string, body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := serve(http.MethodPost, "/api/projects", encoded); response.Code != http.StatusOK {
		t.Fatalf("save status = %d", response.Code)
	}
	runResponse := serve(http.MethodPost, "/api/projects/http-test/run", []byte(`{"startNodeIds":[]}`))
	if runResponse.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", runResponse.Code, runResponse.Body.String())
	}
	var firstRun flow.RunResult
	json.NewDecoder(runResponse.Body).Decode(&firstRun)

	latest := serve(http.MethodGet, "/api/projects/http-test/runs/latest", nil)
	if latest.Code != http.StatusOK {
		t.Fatalf("latest run status = %d", latest.Code)
	}
	var latestEnvelope struct {
		Exists bool           `json:"exists"`
		Run    flow.RunResult `json:"run"`
	}
	json.NewDecoder(latest.Body).Decode(&latestEnvelope)
	if !latestEnvelope.Exists || latestEnvelope.Run.RunID != firstRun.RunID {
		t.Fatalf("latest run not persisted: %#v", latestEnvelope)
	}

	changed := sampleProject()
	changed.Nodes[0].Params["content"] = "id,city" + "\n" + "9,Z"
	changedBytes, _ := json.Marshal(changed)
	if response := serve(http.MethodPost, "/api/projects", changedBytes); response.Code != http.StatusOK {
		t.Fatalf("second save status = %d", response.Code)
	}
	reopenedStore, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	if reopenedStore.runs["http-test"].ProjectVersion == flow.ProjectVersion(reopenedStore.projects["http-test"]) {
		t.Fatal("saved run version should differ from the modified project version")
	}
	if reopenedStore.artifacts["http-test"]["in"].ProjectVersion != firstRun.ProjectVersion {
		t.Fatal("persisted artifact must retain the run project version for reopen comparison")
	}
}
