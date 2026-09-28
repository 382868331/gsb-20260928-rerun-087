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
