package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"dataflow-designer/internal/flow"
)

func TestBlankProjectSaveSerializesEmptyCollections(t *testing.T) {
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	body := `{"format":"dataflow.project/v1","id":"blank","name":"空工程","revision":0,"nodes":null,"edges":null}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("blank save status = %d", response.Code)
	}
	raw := response.Body.String()
	if !strings.Contains(raw, `"nodes":[]`) || !strings.Contains(raw, `"edges":[]`) {
		t.Fatalf("blank project must serialize empty arrays, got %s", raw)
	}
	if strings.Contains(raw, `"issues":null`) {
		t.Fatalf("issues must be [] not null: %s", raw)
	}
}

func multiRecipientProjectForHTTP() flow.Project {
	return flow.Project{
		Format: "dataflow.project/v1", ID: "multi-http", Name: "多接收方",
		Nodes: []flow.Node{
			{ID: "in", Type: "file_input", Name: "输入", Params: map[string]any{"fileName": "a.csv", "format": "csv", "content": "id,city\n1,S\n2,B"}},
			{ID: "f", Type: "transform", Name: "过滤", Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "S"}},
			{ID: "o1", Type: "file_output", Name: "甲", Params: map[string]any{"path": "jia.json", "format": "json", "recipient": "甲"}},
			{ID: "o2", Type: "file_output", Name: "乙", Params: map[string]any{"path": "yi.csv", "format": "csv", "recipient": "乙"}},
		},
		Edges: []flow.Edge{
			{ID: "i-f", Source: "in", Target: "f", SourcePort: "rows", TargetPort: "rows"},
			{ID: "f-o1", Source: "f", Target: "o1", SourcePort: "rows", TargetPort: "rows"},
			{ID: "f-o2", Source: "f", Target: "o2", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func doRun(t *testing.T, handler http.Handler, policy string) flow.RunResult {
	t.Helper()
	body := `{"startNodeIds":[],"publishPolicy":"` + policy + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects/multi-http/run", bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("run status = %d body = %s", response.Code, response.Body.String())
	}
	var result flow.RunResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRunPublishesToMultipleRecipientsAndReopenKeepsEvidence(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	encoded, _ := json.Marshal(multiRecipientProjectForHTTP())
	saveRequest := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(encoded))
	saveResponse := httptest.NewRecorder()
	handler.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("save = %d", saveResponse.Code)
	}

	result := doRun(t, handler, flow.PublishAll)
	if !result.OK {
		t.Fatal("baseline run must succeed")
	}
	for _, nodeID := range []string{"o1", "o2"} {
		if result.Results[nodeID].Visibility != flow.VisibilityPublished {
			t.Fatalf("%s should be published, got %s", nodeID, result.Results[nodeID].Visibility)
		}
	}

	// 同样的内容再跑一次：两个接收方结果版本未变，不能重复发布。
	again := doRun(t, handler, flow.PublishPartial)
	for _, nodeID := range []string{"o1", "o2"} {
		if again.Results[nodeID].Visibility != flow.VisibilityAlready {
			t.Fatalf("%s rerun must not republish identical result, got %s", nodeID, again.Results[nodeID].Visibility)
		}
	}

	// 重新打开：每个接收方看到的仍是其已发布凭证。
	get := httptest.NewRequest(http.MethodGet, "/api/projects/multi-http", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	var reopened struct {
		Project      flow.Project                `json:"project"`
		Artifacts    map[string]flow.Artifact    `json:"artifacts"`
		Publications map[string]flow.Publication `json:"publications"`
	}
	if err := json.NewDecoder(getResponse.Body).Decode(&reopened); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Publications) != 2 {
		t.Fatalf("reopen should expose evidence for both recipients, got %d", len(reopened.Publications))
	}
	if reopened.Publications["o1"].Recipient != "甲" || reopened.Publications["o2"].Recipient != "乙" {
		t.Fatalf("recipients mismatch: %#v", reopened.Publications)
	}
}
