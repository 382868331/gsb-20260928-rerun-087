package flow

import (
	"strings"
	"testing"
)

// publishProject 结构：共享上游 shared 供给两个分支，分别发布给接收方 财务 与 运营。
func publishProject() Project {
	return Project{
		Format: "dataflow.project/v1", ID: "pub", Name: "多接收方发布",
		Nodes: []Node{
			{ID: "shared", Type: "file_input", Name: "共享上游", Params: map[string]any{"fileName": "s.csv", "format": "csv", "content": "id,city,amount\n1,S,10\n2,B,20"}},
			{ID: "fin", Type: "transform", Name: "财务口径", Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "S"}},
			{ID: "ops", Type: "transform", Name: "运营口径", Params: map[string]any{"operation": "passthrough"}},
			{ID: "pub-fin", Type: "publish", Name: "发布财务结果", Params: map[string]any{"receiver": "财务"}},
			{ID: "pub-ops", Type: "publish", Name: "发布运营结果", Params: map[string]any{"receiver": "运营"}},
		},
		Edges: []Edge{
			{ID: "s-fin", Source: "shared", Target: "fin", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-ops", Source: "shared", Target: "ops", SourcePort: "rows", TargetPort: "rows"},
			{ID: "fin-pub", Source: "fin", Target: "pub-fin", SourcePort: "rows", TargetPort: "rows"},
			{ID: "ops-pub", Source: "ops", Target: "pub-ops", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func storedFrom(result RunResult) map[string]Artifact {
	stored := map[string]Artifact{}
	for nodeID, nodeResult := range result.Results {
		if nodeResult.Artifact != nil && (nodeResult.Status == "succeeded" || nodeResult.Status == "reused") {
			stored[nodeID] = *nodeResult.Artifact
		}
	}
	return stored
}

func TestRunComputesDeliveriesForMultipleReceivers(t *testing.T) {
	result := Run(publishProject(), "run-1", nil, nil, nil, t.TempDir())
	if !result.OK {
		t.Fatalf("run failed: %#v", result.Issues)
	}
	fin := result.Deliveries["pub-fin"]
	ops := result.Deliveries["pub-ops"]
	if fin.Pending == nil || fin.Published != nil {
		t.Fatalf("财务结果应仅已计算、未对接收方可见: %#v", fin)
	}
	if ops.Pending == nil || ops.Published != nil {
		t.Fatalf("运营结果应仅已计算、未对接收方可见: %#v", ops)
	}
	if fin.Receiver != "财务" || ops.Receiver != "运营" {
		t.Fatalf("接收方记录错误: %#v %#v", fin.Receiver, ops.Receiver)
	}
	if fin.Pending.Rows != 1 || ops.Pending.Rows != 2 {
		t.Fatalf("各接收方应拿到不同结果: 财务 %d 行, 运营 %d 行", fin.Pending.Rows, ops.Pending.Rows)
	}
}

func TestConfirmAllAndPartialDeliveries(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	// 明确允许部分先行：只确认财务。
	updated, confirmed, err := ConfirmDeliveries(first.Deliveries, []string{"pub-fin"}, false, "run-c1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed) != 1 || confirmed[0] != "pub-fin" {
		t.Fatalf("partial confirm = %v", confirmed)
	}
	if updated["pub-fin"].Published == nil || updated["pub-fin"].Pending != nil {
		t.Fatalf("财务应已对接收方可见: %#v", updated["pub-fin"])
	}
	if updated["pub-ops"].Pending == nil || updated["pub-ops"].Published != nil {
		t.Fatalf("运营应仍为仅已计算: %#v", updated["pub-ops"])
	}
	// 全部共同确认：剩余待确认一起确认。
	final, confirmedAll, err := ConfirmDeliveries(updated, nil, true, "run-c2", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmedAll) != 1 || confirmedAll[0] != "pub-ops" {
		t.Fatalf("confirm all = %v", confirmedAll)
	}
	if final["pub-ops"].Published == nil {
		t.Fatal("运营应已对接收方可见")
	}
	if _, _, err := ConfirmDeliveries(final, nil, true, "run-c3", ProjectVersion(project)); err == nil {
		t.Fatal("没有待确认结果时必须报错")
	}
}

func TestRerunDoesNotRepublishConfirmedDelivery(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	deliveries, _, err := ConfirmDeliveries(first.Deliveries, nil, true, "run-c1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	second := Run(project, "run-2", nil, storedFrom(first), deliveries, t.TempDir())
	if !second.OK {
		t.Fatal("rerun failed")
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops"} {
		state := second.Deliveries[nodeID]
		if state.Published == nil || state.Published.RunID != "run-1" {
			t.Fatalf("%s 已确认部分必须保留原版本: %#v", nodeID, state)
		}
		if state.Pending != nil {
			t.Fatalf("%s 重跑不能重复发布已确认部分: %#v", nodeID, state)
		}
	}
	if !strings.Contains(second.Results["pub-fin"].Reason, "不重复发布") {
		t.Fatalf("应说明不重复发布: %q", second.Results["pub-fin"].Reason)
	}
}

func TestSharedUpstreamChangeRechecksPendingKeepsPublished(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	// 只确认财务，运营保持待确认。
	deliveries, _, err := ConfirmDeliveries(first.Deliveries, []string{"pub-fin"}, false, "run-c1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	publishedFinVersion := deliveries["pub-fin"].Published.Version
	// 共享上游变化：两个分支都受影响。
	project.Nodes[0].Params["content"] = "id,city,amount\n1,S,10\n2,B,20\n3,S,30"
	second := Run(project, "run-2", []string{"shared"}, storedFrom(first), deliveries, t.TempDir())
	if !second.OK {
		t.Fatal("rerun failed")
	}
	// 未发布的运营结果需要重新核对来源：产生新的待确认版本。
	ops := second.Deliveries["pub-ops"]
	if ops.Pending == nil || ops.Pending.RunID != "run-2" {
		t.Fatalf("未发布结果需重新核对来源: %#v", ops)
	}
	// 已发布的财务结果保留原版本，新版本进入待确认而不是覆盖。
	fin := second.Deliveries["pub-fin"]
	if fin.Published == nil || fin.Published.Version != publishedFinVersion {
		t.Fatalf("已发布结果必须保留原版本: %#v", fin)
	}
	if fin.Pending == nil || fin.Pending.RunID != "run-2" {
		t.Fatalf("财务的新版本应仅已计算等待确认: %#v", fin)
	}
	superseded := false
	for _, event := range second.Deliveries["pub-ops"].History {
		if event.Kind == EventSuperseded {
			superseded = true
		}
	}
	if !superseded {
		t.Fatal("旧待确认版本被取代应留下证据")
	}
}

func TestPartialFailureExplainsComputedVsVisible(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	deliveries, _, err := ConfirmDeliveries(first.Deliveries, []string{"pub-fin"}, false, "run-c1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	// 运营分支的转换失败：财务已发布保留，运营遗留的待确认候选失效。
	project.Nodes[2].Params["operation"] = "unknown_op"
	second := Run(project, "run-2", []string{"ops"}, storedFrom(first), deliveries, t.TempDir())
	if second.OK {
		t.Fatal("second run should fail")
	}
	if second.Results["pub-ops"].Status != "blocked" {
		t.Fatalf("失败分支的发布应被阻断: %#v", second.Results["pub-ops"])
	}
	fin := second.Deliveries["pub-fin"]
	if fin.Published == nil || fin.Published.RunID != "run-1" {
		t.Fatal("已对接收方可见的结果不能被失败影响")
	}
	ops := second.Deliveries["pub-ops"]
	if ops.Pending == nil || !ops.Pending.Stale {
		t.Fatalf("失败分支的遗留待确认候选必须标记为已失效: %#v", ops)
	}
	if ops.Pending.StaleReason == "" {
		t.Fatal("失效候选必须说明失去了哪项依据")
	}
	if ops.Published != nil {
		t.Fatal("未确认的失败分支结果不能变成对接收方可见")
	}
	// 本轮失败不能成为发布旧候选的通道：确认必须被拒绝并说明依据。
	if _, _, err := ConfirmDeliveries(second.Deliveries, []string{"pub-ops"}, false, "run-c2", ProjectVersion(project)); err == nil {
		t.Fatal("已失效候选的确认必须被拒绝")
	} else if !strings.Contains(err.Error(), "失效") {
		t.Fatalf("拒绝原因应说明候选已失效: %v", err)
	}
	invalidated := false
	for _, event := range second.Deliveries["pub-ops"].History {
		if event.Kind == EventInvalidated {
			invalidated = true
		}
	}
	if !invalidated {
		t.Fatal("候选失效应留下证据")
	}
}

func TestEmptyOutputPublishesAsEmptyDelivery(t *testing.T) {
	project := publishProject()
	project.Nodes[1].Params["value"] = "NONE"
	result := Run(project, "run-1", nil, nil, nil, t.TempDir())
	if !result.OK {
		t.Fatal("empty output run should succeed")
	}
	fin := result.Deliveries["pub-fin"]
	if fin.Pending == nil || !fin.Pending.Empty || fin.Pending.Rows != 0 {
		t.Fatalf("空输出应标记为空表投递: %#v", fin.Pending)
	}
	confirmed, _, err := ConfirmDeliveries(result.Deliveries, []string{"pub-fin"}, false, "run-c1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	if confirmed["pub-fin"].Published == nil || !confirmed["pub-fin"].Published.Empty {
		t.Fatal("空表投递也应可确认并对齐接收方可见内容")
	}
}

// 全链路：先部分发布 → 共享来源改坏 → 旧候选确认被拒 → 恢复来源重算 → 新候选可确认。
func TestStaleCandidateRejectedUntilFreshRecompute(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	if !first.OK {
		t.Fatal("first run failed")
	}
	// 部分确认：财务先行发布，运营保持待确认。
	deliveries, confirmed, err := ConfirmDeliveries(first.Deliveries, []string{"pub-fin"}, false, "run-c1", ProjectVersion(project))
	if err != nil || len(confirmed) != 1 {
		t.Fatalf("partial confirm: %v %v", confirmed, err)
	}
	publishedFin := deliveries["pub-fin"].Published.Version
	originalContent := project.Nodes[0].Params["content"]

	// 共享来源改成无效内容并重跑：两个分支都被阻断。
	project.Nodes[0].Params["content"] = "\"unclosed-quote"
	second := Run(project, "run-2", nil, storedFrom(first), deliveries, t.TempDir())
	if second.OK {
		t.Fatal("broken shared source must fail the run")
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops"} {
		if second.Results[nodeID].Status != "blocked" {
			t.Fatalf("%s 应被阻断: %#v", nodeID, second.Results[nodeID])
		}
	}
	// 已发布的财务结果保留实际发布事实。
	fin := second.Deliveries["pub-fin"]
	if fin.Published == nil || fin.Published.Version != publishedFin {
		t.Fatalf("已发布历史必须保留: %#v", fin)
	}
	// 运营的遗留待确认候选失效，确认被明确拒绝。
	ops := second.Deliveries["pub-ops"]
	if ops.Pending == nil || !ops.Pending.Stale || ops.Pending.StaleReason == "" {
		t.Fatalf("遗留候选应失效并说明依据: %#v", ops)
	}
	if _, _, err := ConfirmDeliveries(second.Deliveries, []string{"pub-ops"}, false, "run-c2", ProjectVersion(project)); err == nil {
		t.Fatal("本轮失败不能成为发布旧候选的通道")
	}
	// 全部确认也不能夹带失效候选。
	if _, _, err := ConfirmDeliveries(second.Deliveries, nil, true, "run-c3", ProjectVersion(project)); err == nil {
		t.Fatal("只剩失效候选时全部确认必须报错")
	}

	// 来源恢复为相同文字并重算：旧候选不复活，产生全新的待确认版本。
	project.Nodes[0].Params["content"] = originalContent
	third := Run(project, "run-3", nil, storedFrom(first), second.Deliveries, t.TempDir())
	if !third.OK {
		t.Fatal("restored source should run")
	}
	opsAfter := third.Deliveries["pub-ops"]
	if opsAfter.Pending == nil || opsAfter.Pending.Stale || opsAfter.Pending.RunID != "run-3" {
		t.Fatalf("重算成功应产生全新的待确认版本: %#v", opsAfter)
	}
	// 新候选可以确认；接收方看到的是当时获确认的有效结果。
	final, confirmedAll, err := ConfirmDeliveries(third.Deliveries, nil, true, "run-c4", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmedAll) != 1 || confirmedAll[0] != "pub-ops" {
		t.Fatalf("只有运营存在有效新候选: %v", confirmedAll)
	}
	if final["pub-ops"].Published == nil || final["pub-ops"].Published.RunID != "run-3" {
		t.Fatalf("运营应看到 run-3 确认的有效结果: %#v", final["pub-ops"])
	}
	if final["pub-fin"].Published == nil || final["pub-fin"].Published.Version != publishedFin {
		t.Fatalf("财务已发布版本不能被悄悄改变: %#v", final["pub-fin"])
	}
}

// 预览候选后又编辑来源（未重跑）：候选失去流程修订依据，确认必须拒绝。
func TestConfirmRejectsCandidateAfterSourceEditWithoutRerun(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	if !first.OK {
		t.Fatal("first run failed")
	}
	// 候选计算后工程被修改（例如保存了新的来源内容），流程修订已变化。
	project.Nodes[0].Params["content"] = "id,city,amount\n9,Z,99"
	if _, _, err := ConfirmDeliveries(first.Deliveries, []string{"pub-fin"}, false, "run-c1", ProjectVersion(project)); err == nil {
		t.Fatal("编辑来源后旧候选必须被拒绝")
	} else if !strings.Contains(err.Error(), "流程修订") {
		t.Fatalf("拒绝原因应说明失去流程修订依据: %v", err)
	}
	if _, _, err := ConfirmDeliveries(first.Deliveries, nil, true, "run-c2", ProjectVersion(project)); err == nil {
		t.Fatal("全部确认同样必须拒绝失去修订依据的候选")
	}
	// 工程未变化时确认不受影响。
	if _, confirmed, err := ConfirmDeliveries(first.Deliveries, nil, true, "run-c3", ProjectVersion(publishProject())); err != nil || len(confirmed) != 2 {
		t.Fatalf("依据未变时应正常确认: %v %v", confirmed, err)
	}
}
