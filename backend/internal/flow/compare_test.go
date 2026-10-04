package flow

import (
	"encoding/json"
	"reflect"
	"testing"
)

// runAndCollect 执行一次运行并把本轮候选合并进档案（模拟服务端的追加式归档）。
func runAndCollect(t *testing.T, project Project, runID string, stored map[string]Artifact, deliveries map[string]DeliveryState, candidates map[string]Candidate) (RunResult, map[string]Artifact, map[string]DeliveryState, map[string]Candidate) {
	t.Helper()
	result := Run(project, runID, nil, stored, deliveries, t.TempDir())
	stored = storedFrom(result)
	if result.Deliveries != nil {
		deliveries = result.Deliveries
	}
	for _, candidate := range result.Candidates {
		candidates = RecordCandidate(candidates, Node{ID: candidate.NodeID, Params: map[string]any{"receiver": candidate.Receiver}}, Artifact{Version: candidate.Version, Rows: candidate.Rows, Summary: candidate.Summary}, candidate.RunID, candidate.ProjectVersion)
	}
	return result, stored, deliveries, candidates
}

func candidateByRun(t *testing.T, candidates map[string]Candidate, nodeID string, runID string) Candidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.NodeID == nodeID && candidate.RunID == runID {
			return candidate
		}
	}
	t.Fatalf("找不到候选：%s / %s", nodeID, runID)
	return Candidate{}
}

func TestCandidatesArchivedAcrossRunsAndFiltered(t *testing.T) {
	project := publishProject()
	var stored map[string]Artifact
	var deliveries map[string]DeliveryState
	candidates := map[string]Candidate{}

	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261001T100000", stored, deliveries, candidates)

	// 修改共享上游后重跑：产生新候选，旧候选保留不合并。
	project.Nodes[0].Params["content"] = "id,city,amount\n1,S,10\n2,B,20\n3,S,30"
	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261002T100000", stored, deliveries, candidates)

	if len(candidates) != 4 {
		t.Fatalf("两次运行应为 2 个发布节点各留 2 条候选，实际 %d 条", len(candidates))
	}

	// 按接收方筛选。
	finOnly := ListCandidates(candidates, deliveries, CandidateFilter{Receiver: "财务"})
	if len(finOnly) != 2 || finOnly[0].Receiver != "财务" {
		t.Fatalf("按接收方筛选失败: %#v", finOnly)
	}
	// 按运行时间区间筛选（runId 字典序即时间序）。
	ranged := ListCandidates(candidates, deliveries, CandidateFilter{RunFrom: "run-20261002T000000"})
	if len(ranged) != 2 {
		t.Fatalf("按运行时间筛选失败: %#v", ranged)
	}
	// 按发布结果筛选：第二轮重算后旧候选被取代，新候选待确认。
	superseded := ListCandidates(candidates, deliveries, CandidateFilter{Status: StatusSuperseded})
	if len(superseded) != 2 {
		t.Fatalf("旧候选应被取代: %#v", superseded)
	}
	pending := ListCandidates(candidates, deliveries, CandidateFilter{Status: StatusPending})
	if len(pending) != 2 {
		t.Fatalf("新候选应待确认: %#v", pending)
	}
	// 按来源修订筛选：两轮运行的流程修订不同。
	firstRun := candidateByRun(t, candidates, "pub-fin", "run-20261001T100000")
	byRevision := ListCandidates(candidates, deliveries, CandidateFilter{ProjectVersion: firstRun.ProjectVersion})
	if len(byRevision) != 2 {
		t.Fatalf("按来源修订筛选失败: %#v", byRevision)
	}
}

func TestCompareCandidatesRowDiffs(t *testing.T) {
	project := publishProject()
	var stored map[string]Artifact
	var deliveries map[string]DeliveryState
	candidates := map[string]Candidate{}

	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261001T100000", stored, deliveries, candidates)
	// 修改共享上游：id=1 的 amount 变化、id=2 删除、id=3 新增。
	project.Nodes[0].Params["content"] = "id,city,amount\n1,S,99\n3,S,30"
	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261002T100000", stored, deliveries, candidates)

	before := candidateByRun(t, candidates, "pub-ops", "run-20261001T100000")
	after := candidateByRun(t, candidates, "pub-ops", "run-20261002T100000")
	comparison := CompareCandidates(before, after, deliveries)

	if comparison.Identical {
		t.Fatal("输出不同，不应标记为完全相同")
	}
	if len(comparison.Added) != 1 || comparison.Added[0]["id"] != "3" {
		t.Fatalf("新增行不符: %#v", comparison.Added)
	}
	if len(comparison.Removed) != 1 || comparison.Removed[0]["id"] != "2" {
		t.Fatalf("删除行不符: %#v", comparison.Removed)
	}
	if len(comparison.Changes) != 1 || comparison.Changes[0].RowKey != "id=1" {
		t.Fatalf("字段变化应按 id 配对: %#v", comparison.Changes)
	}
	fields := comparison.Changes[0].Fields
	if len(fields) != 1 || fields[0].Field != "amount" || fields[0].A != "10" || fields[0].B != "99" {
		t.Fatalf("字段变化明细不符: %#v", fields)
	}
	// 依据差异应包含来源修订与运行批次。
	basisFields := map[string]bool{}
	for _, diff := range comparison.BasisDiffs {
		basisFields[diff.Field] = true
	}
	if !basisFields["来源修订"] || !basisFields["运行批次"] {
		t.Fatalf("依据差异不完整: %#v", comparison.BasisDiffs)
	}
}

func TestIdenticalOutputDifferentBasisNotMerged(t *testing.T) {
	project := publishProject()
	var stored map[string]Artifact
	var deliveries map[string]DeliveryState
	candidates := map[string]Candidate{}

	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261001T100000", stored, deliveries, candidates)
	// 整流程重跑但来源未变：输出完全相同，运行批次不同。
	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261002T100000", stored, deliveries, candidates)

	first := candidateByRun(t, candidates, "pub-fin", "run-20261001T100000")
	second := candidateByRun(t, candidates, "pub-fin", "run-20261002T100000")
	if first.Key == second.Key {
		t.Fatal("输出相同但运行不同的候选不能合并成同一条记录")
	}
	if len(candidates) != 4 {
		t.Fatalf("输出相同也应各自登记，实际 %d 条", len(candidates))
	}

	comparison := CompareCandidates(first, second, deliveries)
	if !comparison.Identical {
		t.Fatalf("输出完全相同应标记 Identical: %#v", comparison)
	}
	if len(comparison.BasisDiffs) == 0 {
		t.Fatal("输出相同但依据不同，必须明确显示依据差异")
	}
	foundRunDiff := false
	for _, diff := range comparison.BasisDiffs {
		if diff.Field == "运行批次" && diff.A == "run-20261001T100000" && diff.B == "run-20261002T100000" {
			foundRunDiff = true
		}
	}
	if !foundRunDiff {
		t.Fatalf("依据差异应指出运行批次不同: %#v", comparison.BasisDiffs)
	}
}

func TestCompareIsReadOnlyAndStaleStaysStale(t *testing.T) {
	project := publishProject()
	var stored map[string]Artifact
	var deliveries map[string]DeliveryState
	candidates := map[string]Candidate{}

	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261001T100000", stored, deliveries, candidates)
	// 修改工程（不重跑）使待确认候选失效。
	project.Nodes[0].Params["content"] = "id,city,amount\n1,S,10\n2,B,20\n3,S,30"
	deliveries = InvalidatePendingForProject(deliveries, ProjectVersion(project))
	_, stored, deliveries, candidates = runAndCollect(t, project, "run-20261002T100000", stored, deliveries, candidates)

	staleCandidate := candidateByRun(t, candidates, "pub-fin", "run-20261001T100000")
	freshCandidate := candidateByRun(t, candidates, "pub-fin", "run-20261002T100000")
	if got := CandidateStatus(deliveries["pub-fin"], staleCandidate); got != StatusStale {
		t.Fatalf("旧候选应已失效，实际 %s", got)
	}

	// 对比前后投递状态完全一致：对比不改变接收方可见版本，也不恢复失效候选资格。
	before, _ := json.Marshal(deliveries)
	_ = CompareCandidates(staleCandidate, freshCandidate, deliveries)
	_ = ListCandidates(candidates, deliveries, CandidateFilter{})
	after, _ := json.Marshal(deliveries)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("对比操作不得改变投递状态")
	}

	// 失效候选确认仍被拒绝。
	if _, _, err := ConfirmDeliveries(deliveries, []string{"pub-fin"}, false, []ExpectedCandidate{{NodeID: "pub-fin", Version: staleCandidate.Version, RunID: staleCandidate.RunID}}, "run-confirm", ProjectVersion(project)); err == nil {
		t.Fatal("已失效候选的确认必须被拒绝")
	}
	if deliveries["pub-fin"].Published != nil {
		t.Fatal("失效候选不能被发布，接收方可见版本不得改变")
	}
}

func TestCompareViewSaveAndDelete(t *testing.T) {
	views, err := SaveCompareView(nil, CompareView{Name: "财务待确认", Filter: CandidateFilter{Receiver: "财务", Status: StatusPending}, DiffOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	// 同名替换而不是追加。
	views, err = SaveCompareView(views, CompareView{Name: "财务待确认", Filter: CandidateFilter{Receiver: "财务"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].DiffOnly {
		t.Fatalf("同名视图应被替换: %#v", views)
	}
	if _, err := SaveCompareView(views, CompareView{}); err == nil {
		t.Fatal("空名称视图应被拒绝")
	}
	views = DeleteCompareView(views, "财务待确认")
	if len(views) != 0 {
		t.Fatalf("视图应已删除: %#v", views)
	}
}
