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
	stored := map[string]Artifact{}
	for nodeID, result := range first.Results {
		stored[nodeID] = *result.Artifact
	}
	second := Run(project, "run-2", []string{"b"}, stored, t.TempDir())
	if !second.OK {
		t.Fatal("second run should succeed")
	}
	if got := second.Results["a"].Reason; !strings.Contains(got, "沿用") {
		t.Fatalf("unaffected node should explain reuse, got %q", got)
	}
	if got := second.Results["b"].Cause; got != CauseRerunForced || !strings.Contains(second.Results["b"].Reason, "手动触发") {
		t.Fatalf("unchanged start node should be a manual rerun, got %q (%q)", got, second.Results["b"].Reason)
	}
	if got := second.Results["m"].Reason; !strings.Contains(got, "连带") {
		t.Fatalf("downstream should explain cascaded rerun, got %q", got)
	}
}

func TestEmptyArtifactRowsAreNonNull(t *testing.T) {
	project := testProject()
	project.Nodes[2].Params["operation"] = "filter_equals"
	project.Nodes[2].Params["field"] = "city"
	project.Nodes[2].Params["value"] = "NONE"
	result := Run(project, "run-empty", nil, nil, t.TempDir())
	if !result.OK {
		t.Fatalf("empty filter result should still succeed: %#v", result.Issues)
	}
	rows := result.Results["m"].Artifact.Rows
	if rows == nil {
		t.Fatal("artifact rows must be an empty slice, not nil")
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestSharedUpstreamEmptyAndLocalFailureCannotReuseOldSuccess(t *testing.T) {
	// 结构：共享上游 shared 同时供给 down1 / down2；down2 还有自己的输入 side。
	project := Project{
		Format: "dataflow.project/v1", ID: "share", Name: "share",
		Nodes: []Node{
			{ID: "shared", Type: "file_input", Name: "共享上游", Params: map[string]any{"fileName": "s.csv", "format": "csv", "content": "id,city\n1,S"}},
			{ID: "side", Type: "file_input", Name: "旁路输入", Params: map[string]any{"fileName": "b.csv", "format": "csv", "content": "id,city\n2,S"}},
			{ID: "down1", Type: "transform", Name: "共享下游", Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "NONE"}},
			{ID: "down2", Type: "transform", Name: "汇合下游", Params: map[string]any{"operation": "passthrough"}},
			{ID: "out", Type: "file_output", Name: "输出", Params: map[string]any{"path": "o.json", "format": "json"}},
		},
		Edges: []Edge{
			{ID: "s-d1", Source: "shared", Target: "down1", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-d2", Source: "shared", Target: "down2", SourcePort: "rows", TargetPort: "rows"},
			{ID: "b-d2", Source: "side", Target: "down2", SourcePort: "rows", TargetPort: "rows"},
			{ID: "d2-o", Source: "down2", Target: "out", SourcePort: "rows", TargetPort: "rows"},
		},
	}
	first := Run(project, "run-1", nil, nil, t.TempDir())
	if !first.OK {
		t.Fatalf("baseline run failed: %#v", first.Issues)
	}
	stored := map[string]Artifact{}
	for nodeID, nodeResult := range first.Results {
		stored[nodeID] = *nodeResult.Artifact
	}

	// 从共享上游做局部重跑：过滤后是空表（0 行），同时把旁路输入改成无法解析的内容制造局部失败。
	project.Nodes[0].Params["content"] = "id,city\n1,ZZZ"
	project.Nodes[1].Params["format"] = "json"
	project.Nodes[1].Params["content"] = "not-json"
	second := Run(project, "run-2", []string{"shared", "side"}, stored, t.TempDir())
	if second.OK {
		t.Fatal("second run should fail due to side input parse error")
	}
	// down1 本轮成功但是空输出；必须带本轮新产物（0 行），不能展示旧的 1 行成功数据。
	if second.Results["down1"].Status != "succeeded" {
		t.Fatalf("down1 should succeed with empty rows, got %s", second.Results["down1"].Status)
	}
	if rows := len(second.Results["down1"].Artifact.Rows); rows != 0 {
		t.Fatalf("down1 must carry this run empty output (0 rows), got %d old rows", rows)
	}
	// down2 同时依赖失败的 side，必须被阻断且不能暴露旧产物；out 同样阻断。
	for _, nodeID := range []string{"down2", "out"} {
		if second.Results[nodeID].Status != "blocked" || second.Results[nodeID].Artifact != nil {
			t.Fatalf("%s must be blocked without old artifact: %#v", nodeID, second.Results[nodeID])
		}
	}
}

func TestRerunCausesDistinguishParamsAndInputs(t *testing.T) {
	project := testProject()
	first := Run(project, "run-1", nil, nil, t.TempDir())
	stored := map[string]Artifact{}
	for nodeID, nodeResult := range first.Results {
		stored[nodeID] = *nodeResult.Artifact
	}
	project.Nodes[1].Params["content"] = "id,city\n3,S\n4,S"
	second := Run(project, "run-2", []string{"b"}, stored, t.TempDir())
	if !second.OK {
		t.Fatal("second run failed")
	}
	if cause := second.Results["b"].Cause; cause != CauseRerunParams {
		t.Fatalf("start node cause = %q, want %q", cause, CauseRerunParams)
	}
	if cause := second.Results["m"].Cause; cause != CauseRerunInput {
		t.Fatalf("downstream cause = %q, want %q", cause, CauseRerunInput)
	}
	if cause := second.Results["a"].Cause; cause != CauseReused {
		t.Fatalf("unaffected cause = %q, want %q", cause, CauseReused)
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
