package flow

import (
	"os"
	"strings"
	"testing"
)

// 多接收方工程：共享上游 shared 同时供给 out1 / out2 两个接收方，out2 还有自己的旁路输入 side。
func multiRecipientProject() Project {
	return Project{
		Format: "dataflow.project/v1", ID: "multi", Name: "多接收方",
		Nodes: []Node{
			{ID: "shared", Type: "file_input", Name: "共享输入", Params: map[string]any{"fileName": "s.csv", "format": "csv", "content": "id,city\n1,S\n2,B"}},
			{ID: "side", Type: "file_input", Name: "旁路输入", Params: map[string]any{"fileName": "b.csv", "format": "csv", "content": "id,city\n3,S"}},
			{ID: "t1", Type: "transform", Name: "上海过滤", Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "S"}},
			{ID: "t2", Type: "transform", Name: "汇合", Params: map[string]any{"operation": "passthrough"}},
			{ID: "out1", Type: "file_output", Name: "接收方甲", Params: map[string]any{"path": "jia.json", "format": "json", "recipient": "接收方甲"}},
			{ID: "out2", Type: "file_output", Name: "接收方乙", Params: map[string]any{"path": "yi.json", "format": "json", "recipient": "接收方乙"}},
		},
		Edges: []Edge{
			{ID: "s-t1", Source: "shared", Target: "t1", SourcePort: "rows", TargetPort: "rows"},
			{ID: "t1-o1", Source: "t1", Target: "out1", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-t2", Source: "shared", Target: "t2", SourcePort: "rows", TargetPort: "rows"},
			{ID: "b-t2", Source: "side", Target: "t2", SourcePort: "rows", TargetPort: "rows"},
			{ID: "t2-o2", Source: "t2", Target: "out2", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func firstSuccessfulRun(t *testing.T, project Project, root string) (map[string]Artifact, map[string]Publication) {
	t.Helper()
	result := RunWithOptions(project, "run-1", Options{Policy: PublishAll, OutputRoot: root, Now: "2026-10-02T00:00:00Z"})
	if !result.OK {
		t.Fatalf("baseline run must succeed: %#v", result.Results)
	}
	for _, nodeID := range []string{"out1", "out2"} {
		if result.Results[nodeID].Visibility != VisibilityPublished {
			t.Fatalf("%s should be published under all policy, got %s", nodeID, result.Results[nodeID].Visibility)
		}
	}
	if _, err := os.Stat(result.Publications["out1"].Path); err != nil {
		t.Fatalf("published file for out1 should exist: %v", err)
	}
	artifacts := map[string]Artifact{}
	for nodeID, nodeResult := range result.Results {
		if nodeResult.Artifact != nil {
			artifacts[nodeID] = *nodeResult.Artifact
		}
	}
	return artifacts, result.Publications
}

func TestAllPolicyHoldsEveryPublicationWhenOneBranchFails(t *testing.T) {
	project := multiRecipientProject()
	root := t.TempDir()
	_, publications := firstSuccessfulRun(t, project, root)

	// 让 out2 的旁路输入失败；同时把 t1 过滤条件改成无匹配（0 行空输出），但共同确认策略下必须全部保持仅已计算。
	project.Nodes[2].Params["value"] = "NONE"
	project.Nodes[1].Params["format"] = "json"
	project.Nodes[1].Params["content"] = "not-json"
	result := RunWithOptions(project, "run-2", Options{Policy: PublishAll, Stored: map[string]Artifact{}, Publications: publications, OutputRoot: root, Now: "2026-10-02T01:00:00Z"})
	if result.OK {
		t.Fatal("run should fail because the side input cannot be parsed")
	}
	out1 := result.Results["out1"]
	if out1.Visibility != VisibilityComputed || out1.Artifact == nil {
		t.Fatalf("out1 should be computed-only but still carry this-run artifact: %#v", out1)
	}
	if rows := len(out1.Artifact.Rows); rows != 0 {
		t.Fatalf("out1 should be a legal empty output (0 rows), got %d", rows)
	}
	if out1.Published == nil || out1.Published.Version != publications["out1"].Version {
		t.Fatalf("out1 must still expose the previously published version as evidence: %#v", out1)
	}
	out2 := result.Results["out2"]
	if out2.Visibility != VisibilityPrevious || out2.Published == nil {
		t.Fatalf("out2 must report the previously published version is still visible: %#v", out2)
	}
	// 凭证集合保持上一版本，没有任何新发布。
	for _, nodeID := range []string{"out1", "out2"} {
		if result.Publications[nodeID].RunID != "run-1" {
			t.Fatalf("%s publication must remain run-1, got %s", nodeID, result.Publications[nodeID].RunID)
		}
	}
}

func TestPartialPolicyPublishesHealthyBranchAndKeepsPreviousEvidence(t *testing.T) {
	project := multiRecipientProject()
	root := t.TempDir()
	artifacts, publications := firstSuccessfulRun(t, project, root)

	project.Nodes[1].Params["format"] = "json"
	project.Nodes[1].Params["content"] = "not-json"
	result := RunWithOptions(project, "run-2", Options{Policy: PublishPartial, Stored: artifacts, Publications: publications, OutputRoot: root, Now: "2026-10-02T02:00:00Z"})
	if result.OK {
		t.Fatal("run should fail due to broken side input")
	}
	// out1 依赖共享输入，未变化 → 版本相同 → already，不重复发布。
	if got := result.Results["out1"].Visibility; got != VisibilityAlready {
		t.Fatalf("out1 should not be republished, got %s", got)
	}
	if got := result.Publications["out1"].RunID; got != "run-1" {
		t.Fatalf("out1 publication evidence must stay run-1, got %s", got)
	}
	// out2 分支失败：接收方乙仍可见 run-1 的旧版本内容。
	out2 := result.Results["out2"]
	if out2.Visibility != VisibilityPrevious || out2.Published == nil || out2.Published.RunID != "run-1" {
		t.Fatalf("out2 should keep previous visible evidence: %#v", out2)
	}
}

func TestChangedSharedUpstreamRepublishesUnaffectedRecipientOnlyAfterCompute(t *testing.T) {
	project := multiRecipientProject()
	root := t.TempDir()
	artifacts, publications := firstSuccessfulRun(t, project, root)

	// 共享上游新增一行：两个接收方的结果版本都变了，partial 策略下两边都应重新发布新版本。
	project.Nodes[0].Params["content"] = "id,city\n1,S\n2,B\n4,S"
	result := RunWithOptions(project, "run-3", Options{Policy: PublishPartial, Stored: artifacts, Publications: publications, OutputRoot: root, Now: "2026-10-02T03:00:00Z"})
	if !result.OK {
		t.Fatalf("run should succeed: %#v", result.Results)
	}
	for _, nodeID := range []string{"out1", "out2"} {
		entry := result.Results[nodeID]
		if entry.Visibility != VisibilityPublished || entry.Published.RunID != "run-3" {
			t.Fatalf("%s should be republished at run-3 after shared upstream change: %#v", nodeID, entry)
		}
		if entry.Published.Version == publications[nodeID].Version {
			t.Fatalf("%s version must be re-derived from the new shared upstream", nodeID)
		}
	}
	if rows := len(result.Publications["out1"].Rows); rows != 2 {
		t.Fatalf("out1 should publish 2 Shanghai rows, got %d", rows)
	}
	if rows := len(result.Publications["out2"].Rows); rows != 4 {
		t.Fatalf("out2 should publish 4 merged rows, got %d", rows)
	}
}

func TestRerunDoesNotRepublishConfirmedIdenticalVersion(t *testing.T) {
	project := multiRecipientProject()
	root := t.TempDir()
	artifacts, publications := firstSuccessfulRun(t, project, root)

	// 先让 out2 失败一次（partial），然后修复，重跑：out1 版本未变必须 already，out2 重新发布。
	project.Nodes[1].Params["format"] = "json"
	project.Nodes[1].Params["content"] = "not-json"
	broken := RunWithOptions(project, "run-x", Options{Policy: PublishPartial, Stored: artifacts, Publications: publications, OutputRoot: root, Now: "2026-10-02T04:00:00Z"})
	if broken.OK {
		t.Fatal("broken run should fail")
	}
	if broken.Publications["out2"].RunID != "run-1" {
		t.Fatal("failure must not erase published evidence")
	}
	project.Nodes[1].Params["format"] = "csv"
	project.Nodes[1].Params["content"] = "id,city\n3,S\n5,S"
	healed := RunWithOptions(project, "run-y", Options{Policy: PublishPartial, Stored: artifacts, Publications: broken.Publications, OutputRoot: root, Now: "2026-10-02T05:00:00Z"})
	if !healed.OK {
		t.Fatalf("healed run should succeed: %#v", healed.Results)
	}
	if healed.Results["out1"].Visibility != VisibilityAlready {
		t.Fatalf("out1 must not be republished: %#v", healed.Results["out1"])
	}
	if healed.Results["out2"].Visibility != VisibilityPublished || healed.Results["out2"].Published.RunID != "run-y" {
		t.Fatalf("out2 should be freshly published after recovery: %#v", healed.Results["out2"])
	}
	if rows := len(healed.Publications["out2"].Rows); rows != 4 {
		t.Fatalf("out2 should publish recomputed 4 rows, got %d", rows)
	}
}

func TestEmptyOutputCanBePublishedToRecipient(t *testing.T) {
	project := multiRecipientProject()
	project.Nodes[2].Params["value"] = "NONE"
	result := RunWithOptions(project, "run-empty", Options{Policy: PublishAll, OutputRoot: t.TempDir(), Now: "2026-10-02T06:00:00Z"})
	if !result.OK {
		t.Fatalf("empty output run must succeed: %#v", result.Issues)
	}
	out1 := result.Results["out1"]
	if out1.Visibility != VisibilityPublished || len(out1.Artifact.Rows) != 0 {
		t.Fatalf("empty 0-row output must be a valid published result: %#v", out1)
	}
	if !strings.Contains(out1.Reason, "0 行") {
		t.Fatalf("publish reason should state 0 rows, got %q", out1.Reason)
	}
}

func TestBlankProjectNormalizesAndRuns(t *testing.T) {
	blank := Project{Format: "dataflow.project/v1", ID: "blank", Name: "空工程"}
	blank.Normalize()
	if blank.Nodes == nil || blank.Edges == nil {
		t.Fatal("Normalize must provide empty slices")
	}
	result := RunWithOptions(blank, "run-blank", Options{Policy: PublishAll, OutputRoot: t.TempDir(), Now: "now"})
	if !result.OK || len(result.Results) != 0 || result.Publications == nil {
		t.Fatalf("blank project should run cleanly with empty collections: %#v", result)
	}
}
