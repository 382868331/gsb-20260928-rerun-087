package flow

import (
	"strings"
	"testing"
)

func testProject() Project {
	return Project{
		Format: "dataflow.project/v1", ID: "test", Name: "test",
		Nodes: []Node{
			{ID: "a", Type: "file_input", Name: "A", Params: map[string]any{"fileName": "a.csv", "format": "csv", "content": "id,city\n1,S\n2,B"}},
			{ID: "b", Type: "file_input", Name: "B", Params: map[string]any{"fileName": "b.csv", "format": "csv", "content": "id,city\n3,S"}},
			{ID: "m", Type: "transform", Name: "汇合", Params: map[string]any{"operation": "passthrough"}},
			{ID: "o", Type: "file_output", Name: "输出", Params: map[string]any{"path": "out.json", "format": "json"}},
		},
		Edges: []Edge{
			{ID: "am", Source: "a", Target: "m", SourcePort: "rows", TargetPort: "rows"},
			{ID: "bm", Source: "b", Target: "m", SourcePort: "rows", TargetPort: "rows"},
			{ID: "mo", Source: "m", Target: "o", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func TestBranchMergeExecution(t *testing.T) {
	result := Run(testProject(), "run-1", nil, nil, t.TempDir())
	if !result.OK {
		t.Fatalf("expected success: %#v", result.Issues)
	}
	if got := len(result.Results["m"].Artifact.Rows); got != 3 {
		t.Fatalf("merge got %d rows, want 3", got)
	}
}

func TestCycleRejectedBeforeRun(t *testing.T) {
	project := testProject()
	project.Edges = append(project.Edges, Edge{ID: "cycle", Source: "o", Target: "a", SourcePort: "rows", TargetPort: "rows"})
	validation := Validate(project)
	if validation.OK {
		t.Fatal("expected cycle validation failure")
	}
	result := Run(project, "run-cycle", nil, nil, t.TempDir())
	if result.OK || len(result.Issues) == 0 {
		t.Fatal("cycle must prevent run start")
	}
}

func TestFailureBlocksDownstreamAndRemovesOldArtifact(t *testing.T) {
	project := testProject()
	stored := make(map[string]Artifact)
	first := Run(project, "run-1", nil, nil, t.TempDir())
	for nodeID, result := range first.Results {
		if result.Artifact != nil {
			stored[nodeID] = *result.Artifact
		}
	}
	project.Nodes[0].Params["content"] = "not-json"
	project.Nodes[0].Params["format"] = "json"
	second := Run(project, "run-2", []string{"a"}, stored, t.TempDir())
	if second.OK {
		t.Fatal("invalid input should fail")
	}
	if second.Results["m"].Status != "blocked" || second.Results["o"].Status != "blocked" {
		t.Fatalf("downstream must be blocked: %#v", second.Results)
	}
	if second.Results["m"].Artifact != nil || second.Results["o"].Artifact != nil {
		t.Fatal("failed run must not expose old downstream artifacts")
	}
}

func TestLocalRunReusesUnaffectedBranch(t *testing.T) {
	project := testProject()
	first := Run(project, "run-1", nil, nil, t.TempDir())
	stored := map[string]Artifact{}
	for nodeID, result := range first.Results {
		stored[nodeID] = *result.Artifact
	}
	project.Nodes[1].Params["content"] = "id,city\n3,S\n4,S"
	second := Run(project, "run-2", []string{"b"}, stored, t.TempDir())
	if !second.OK {
		t.Fatalf("local run failed")
	}
	if second.Results["a"].Status != "reused" {
		t.Fatalf("unaffected node should be reused, got %s", second.Results["a"].Status)
	}
	if got := len(second.Results["m"].Artifact.Rows); got != 4 {
		t.Fatalf("local merge got %d rows, want 4", got)
	}
}

func TestRunReportsReuseReasons(t *testing.T) {
	project := testProject()
	first := Run(project, "run-1", nil, nil, t.TempDir())
	if !first.OK {
		t.Fatal("first run should succeed")
	}
	stored := firstRunArtifacts(t, project)
	second := Run(project, "run-2", []string{"b"}, stored, t.TempDir())
	if !second.OK {
		t.Fatal("second run should succeed")
	}
	if got := second.Results["a"].Reason; !strings.Contains(got, "沿用") {
		t.Fatalf("unaffected node should explain reuse, got %q", got)
	}
	if got := second.Results["b"].Reason; !strings.Contains(got, "重跑入口") {
		t.Fatalf("start node should explain local rerun, got %q", got)
	}
	if got := second.Results["m"].ChangeBasis; got != "downstream" {
		t.Fatalf("merge rerun basis = %q, want downstream", got)
	}
	if got := second.Results["m"].Reason; !strings.Contains(got, "连带重新执行") {
		t.Fatalf("downstream should explain cascade, got %q", got)
	}
}

func firstRunArtifacts(t *testing.T, project Project) map[string]Artifact {
	t.Helper()
	first := Run(project, "run-1", nil, nil, t.TempDir())
	if !first.OK {
		t.Fatalf("first run failed: %#v", first.Issues)
	}
	stored := map[string]Artifact{}
	for nodeID, result := range first.Results {
		stored[nodeID] = *result.Artifact
	}
	return stored
}

func TestRevisionDistinguishesParamAndInputChanges(t *testing.T) {
	project := testProject()

	// Changing only the start node params: start node = start basis, merge = inputs.
	byInput := testProject()
	byInput.Nodes[1].Params["content"] = "id,city\n3,S\n4,S"
	resultInput := Run(byInput, "run-2", []string{"b"}, firstRunArtifacts(t, project), t.TempDir())
	if !resultInput.OK {
		t.Fatalf("input-driven rerun failed: %#v", resultInput.Results)
	}
	if got := resultInput.Results["m"].ChangeBasis; got != "inputs" {
		t.Fatalf("merge basis = %q, want inputs", got)
	}

	// Re-running from m with only m params changed, upstream inputs reused unchanged: m = params.
	byParams := testProject()
	byParams.Nodes[2].Params["operation"] = "uppercase"
	byParams.Nodes[2].Params["field"] = "city"
	resultParams := Run(byParams, "run-3", []string{"m"}, firstRunArtifacts(t, project), t.TempDir())
	if !resultParams.OK {
		t.Fatalf("param-driven rerun failed: %#v", resultParams.Results)
	}
	if got := resultParams.Results["m"].ChangeBasis; got != "params" {
		t.Fatalf("m basis = %q, want params", got)
	}
	if got := resultParams.Results["m"].Reason; !strings.Contains(got, "参数已变化") {
		t.Fatalf("m reason = %q, want param change explanation", got)
	}
	if got := resultParams.Results["o"].ChangeBasis; got != "inputs" {
		t.Fatalf("o basis = %q, want inputs", got)
	}
}

// Shared upstream that yields zero rows together with one failed branch must
// never let the shared downstream serve its old successful artifact.
func TestEmptyOutputAndFailedBranchBlockSharedDownstream(t *testing.T) {
	project := testProject()
	stored := firstRunArtifacts(t, project)
	oldMerge := stored["m"]
	if len(oldMerge.Rows) != 3 {
		t.Fatalf("baseline merge rows = %d, want 3", len(oldMerge.Rows))
	}

	// b filters its only row away (empty output); a also breaks in the same rerun.
	project.Nodes[0].Params["format"] = "json"
	project.Nodes[0].Params["content"] = "not-json"
	project.Nodes[1].Params["content"] = "id,city\n9,Hangzhou"
	project.Nodes = append(project.Nodes, Node{
		ID: "f", Type: "transform", Name: "过滤空",
		Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "Shanghai"},
	})
	project.Edges = []Edge{
		{ID: "am", Source: "a", Target: "m", SourcePort: "rows", TargetPort: "rows"},
		{ID: "bf", Source: "b", Target: "f", SourcePort: "rows", TargetPort: "rows"},
		{ID: "fm", Source: "f", Target: "m", SourcePort: "rows", TargetPort: "rows"},
		{ID: "mo", Source: "m", Target: "o", SourcePort: "rows", TargetPort: "rows"},
	}
	second := Run(project, "run-2", nil, stored, t.TempDir())
	if second.OK {
		t.Fatal("rerun with failing branch must not be OK")
	}
	if second.Results["a"].Status != "failed" {
		t.Fatalf("a status = %s, want failed", second.Results["a"].Status)
	}
	if second.Results["f"].Status != "succeeded" || len(second.Results["f"].Artifact.Rows) != 0 {
		t.Fatalf("filter should succeed with zero rows: %#v", second.Results["f"])
	}
	if second.Results["m"].Status != "blocked" || second.Results["m"].Artifact != nil {
		t.Fatalf("shared merge must be blocked without old artifact: %#v", second.Results["m"])
	}
	if second.Results["o"].Status != "blocked" || second.Results["o"].Artifact != nil {
		t.Fatalf("shared output must be blocked without old artifact: %#v", second.Results["o"])
	}
}

func TestParametersAndDataEdgesAreDistinct(t *testing.T) {
	project := testProject()
	validation := Validate(project)
	if !validation.OK {
		t.Fatalf("valid project: %#v", validation.Issues)
	}
	project.Nodes[0].Params["fileName"] = ""
	validation = Validate(project)
	if validation.OK || !strings.Contains(validation.Issues[0].Message, "文件名") {
		t.Fatalf("parameter validation should identify missing 文件名: %#v", validation.Issues)
	}
}
