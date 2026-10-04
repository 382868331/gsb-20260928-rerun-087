package flow

import (
	"sort"
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

// oneTarget 构造绑定到页面实际看到的候选版本与运行批次的确认目标。
func oneTarget(deliveries map[string]DeliveryState, nodeID string) ConfirmTarget {
	pending := deliveries[nodeID].Pending
	if pending == nil {
		return ConfirmTarget{NodeID: nodeID}
	}
	return ConfirmTarget{NodeID: nodeID, Version: pending.Version, RunID: pending.RunID}
}

// allTargets 收集当前全部待确认候选（共同确认必须覆盖每一个分支）。
func allTargets(deliveries map[string]DeliveryState) []ConfirmTarget {
	nodeIDs := make([]string, 0)
	for nodeID, state := range deliveries {
		if state.Pending != nil {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	sort.Strings(nodeIDs)
	targets := make([]ConfirmTarget, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		targets = append(targets, oneTarget(deliveries, nodeID))
	}
	return targets
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
	updated, confirmed, err := ConfirmDeliveries(first.Deliveries, []ConfirmTarget{oneTarget(first.Deliveries, "pub-fin")}, false, "run-c1", ProjectVersion(project))
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
	final, confirmedAll, err := ConfirmDeliveries(updated, allTargets(updated), true, "run-c2", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmedAll) != 1 || confirmedAll[0] != "pub-ops" {
		t.Fatalf("confirm all = %v", confirmedAll)
	}
	if final["pub-ops"].Published == nil {
		t.Fatal("运营应已对接收方可见")
	}
	if _, _, err := ConfirmDeliveries(final, allTargets(final), true, "run-c3", ProjectVersion(project)); err == nil {
		t.Fatal("没有待确认结果时必须报错")
	}
}

func TestRerunDoesNotRepublishConfirmedDelivery(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	deliveries, _, err := ConfirmDeliveries(first.Deliveries, allTargets(first.Deliveries), true, "run-c1", ProjectVersion(project))
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
	deliveries, _, err := ConfirmDeliveries(first.Deliveries, []ConfirmTarget{oneTarget(first.Deliveries, "pub-fin")}, false, "run-c1", ProjectVersion(project))
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
	deliveries, _, err := ConfirmDeliveries(first.Deliveries, []ConfirmTarget{oneTarget(first.Deliveries, "pub-fin")}, false, "run-c1", ProjectVersion(project))
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
	if _, _, err := ConfirmDeliveries(second.Deliveries, []ConfirmTarget{oneTarget(second.Deliveries, "pub-ops")}, false, "run-c2", ProjectVersion(project)); err == nil {
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
	confirmed, _, err := ConfirmDeliveries(result.Deliveries, []ConfirmTarget{oneTarget(result.Deliveries, "pub-fin")}, false, "run-c1", ProjectVersion(project))
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
	deliveries, confirmed, err := ConfirmDeliveries(first.Deliveries, []ConfirmTarget{oneTarget(first.Deliveries, "pub-fin")}, false, "run-c1", ProjectVersion(project))
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
	if _, _, err := ConfirmDeliveries(second.Deliveries, []ConfirmTarget{oneTarget(second.Deliveries, "pub-ops")}, false, "run-c2", ProjectVersion(project)); err == nil {
		t.Fatal("本轮失败不能成为发布旧候选的通道")
	}
	// 全部确认也不能夹带失效候选。
	if _, _, err := ConfirmDeliveries(second.Deliveries, allTargets(second.Deliveries), true, "run-c3", ProjectVersion(project)); err == nil {
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
	final, confirmedAll, err := ConfirmDeliveries(third.Deliveries, allTargets(third.Deliveries), true, "run-c4", ProjectVersion(project))
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
	rejected, _, err := ConfirmDeliveries(first.Deliveries, []ConfirmTarget{oneTarget(first.Deliveries, "pub-fin")}, false, "run-c1", ProjectVersion(project))
	if err == nil {
		t.Fatal("编辑来源后旧候选必须被拒绝")
	} else if !strings.Contains(err.Error(), "流程修订") {
		t.Fatalf("拒绝原因应说明失去流程修订依据: %v", err)
	}
	// 拒绝是持久状态：即使把来源改回原文（工程版本恢复成候选依据）且不重跑，
	// 旧候选也只能留作比较，不能重新确认。
	restored := publishProject()
	if _, _, err := ConfirmDeliveries(rejected, []ConfirmTarget{oneTarget(rejected, "pub-fin")}, false, "run-c1b", ProjectVersion(restored)); err == nil {
		t.Fatal("来源恢复原文而不重跑，被拒绝的候选不能恢复发布资格")
	}
	if len(rejected["pub-fin"].Invalidated) == 0 {
		t.Fatal("被拒绝候选必须转入留证列表，仅供查阅比较")
	}
	hasRejectedEvent := false
	for _, event := range rejected["pub-fin"].History {
		if event.Kind == EventRejected {
			hasRejectedEvent = true
		}
	}
	if !hasRejectedEvent {
		t.Fatal("每次拒绝都必须留下可追溯证据")
	}
	if _, _, err := ConfirmDeliveries(rejected, allTargets(rejected), true, "run-c2", ProjectVersion(project)); err == nil {
		t.Fatal("全部确认同样必须拒绝失去修订依据的候选")
	}
	// 工程未变化时确认不受影响。
	fresh := Run(restored, "run-2", nil, storedFrom(first), rejected, t.TempDir())
	if !fresh.OK {
		t.Fatal("恢复原文后重算应成功")
	}
	if _, confirmed, err := ConfirmDeliveries(fresh.Deliveries, allTargets(fresh.Deliveries), true, "run-c3", ProjectVersion(restored)); err != nil || len(confirmed) != 2 {
		t.Fatalf("依据未变时应正常确认: %v %v", confirmed, err)
	}
}

// 两个页面分别查看旧候选与新候选：旧页面迟到的确认携带旧版本/批次，
// 不能误发布重算出的新结果；重复请求幂等，不产生第二条已发布证据。
func TestStaleConfirmFromOldWindowCannotPublishNewCandidate(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	oldTarget := oneTarget(first.Deliveries, "pub-fin")
	if oldTarget.Version == "" || oldTarget.RunID != "run-1" {
		t.Fatalf("旧页面应记住候选版本与批次: %#v", oldTarget)
	}
	// 新窗口修改共享来源并重算：新候选来自 run-2、新版本。
	project.Nodes[0].Params["content"] = "id,city,amount\n1,S,10\n2,B,20\n3,S,30"
	second := Run(project, "run-2", nil, storedFrom(first), first.Deliveries, t.TempDir())
	newTarget := oneTarget(second.Deliveries, "pub-fin")
	if newTarget.Version == oldTarget.Version || newTarget.RunID != "run-2" {
		t.Fatalf("重算应产生新版本/批次的候选: old=%#v new=%#v", oldTarget, newTarget)
	}
	// 旧页面迟到的确认（绑定 run-1 旧版本）必须拒绝，不能发布 run-2 新结果。
	rejected, _, err := ConfirmDeliveries(second.Deliveries, []ConfirmTarget{oldTarget}, false, "confirm-late", ProjectVersion(project))
	if err == nil {
		t.Fatal("旧页面迟到的确认必须被拒绝")
	}
	if rejected["pub-fin"].Published != nil {
		t.Fatal("迟到确认不能让任何结果对接收方可见")
	}
	if rejected["pub-fin"].Pending == nil || rejected["pub-fin"].Pending.RunID != "run-2" {
		t.Fatal("被拒绝的应是旧确认，新候选必须保留待确认")
	}
	// 新页面用正确批次确认成功；再重放旧确认仍被拒绝，重放相同确认则幂等。
	confirmed, published, err := ConfirmDeliveries(rejected, []ConfirmTarget{newTarget}, false, "confirm-new", ProjectVersion(project))
	if err != nil || len(published) != 1 {
		t.Fatalf("新批次确认应成功: %v %v", published, err)
	}
	if confirmed["pub-fin"].Published.Version != newTarget.Version {
		t.Fatal("接收方只能看到确认时绑定的新候选版本")
	}
	if _, _, err := ConfirmDeliveries(confirmed, []ConfirmTarget{oldTarget}, false, "confirm-late-2", ProjectVersion(project)); err == nil {
		t.Fatal("发布后旧批次确认依旧必须拒绝")
	}
	idempotent, again, err := ConfirmDeliveries(confirmed, []ConfirmTarget{newTarget}, false, "confirm-repeat", ProjectVersion(project))
	if err != nil || len(again) != 0 {
		t.Fatalf("重复确认已发布版本应幂等无操作: %v %v", again, err)
	}
	publishedEvents := 0
	for _, event := range idempotent["pub-fin"].History {
		if event.Kind == EventPublished {
			publishedEvents++
		}
	}
	if publishedEvents != 1 {
		t.Fatalf("重复请求不能重复发布/留多条已发布证据，得到 %d 条", publishedEvents)
	}
}

// 共同确认遇到一条失效分支必须整组失败，不能部分发布其他有效分支。
func TestConfirmAllIsAtomicWhenOneBranchStale(t *testing.T) {
	project := publishProject()
	first := Run(project, "run-1", nil, nil, nil, t.TempDir())
	// 运营分支上游改坏并重跑：运营候选失效；财务分支在失败的整流程运行中也被阻断。
	project.Nodes[2].Params["operation"] = "unknown_op"
	second := Run(project, "run-2", []string{"ops"}, storedFrom(first), first.Deliveries, t.TempDir())
	if second.OK {
		t.Fatal("second run should fail")
	}
	// 财务不在受影响范围、产物被沿用，但运行结果里财务没有新候选；构造一个财务仍有
	// 有效待确认、运营已失效的混合状态：先恢复财务待确认（它本就没被失效）。
	mixed := second.Deliveries
	if mixed["pub-fin"].Pending == nil || mixed["pub-fin"].Pending.Stale {
		t.Fatalf("财务候选应保持有效待确认: %#v", mixed["pub-fin"])
	}
	if mixed["pub-ops"].Pending == nil || !mixed["pub-ops"].Pending.Stale {
		t.Fatalf("运营候选应已失效: %#v", mixed["pub-ops"])
	}
	// 用户在页面上看到两个接收方卡片，共同确认同时提交两者；任一失效必须整组拒绝。
	if _, published, err := ConfirmDeliveries(mixed, allTargets(mixed), true, "confirm-all", ProjectVersion(project)); err == nil {
		t.Fatal("共同确认夹带失效分支时必须失败")
	} else if len(published) != 0 {
		t.Fatalf("失败时不能部分发布: %v", published)
	}
	if mixed["pub-fin"].Pending == nil || mixed["pub-fin"].Published != nil {
		t.Fatal("有效的财务分支也不能被部分发布")
	}
}
