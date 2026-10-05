package flow

import (
	"strings"
	"testing"
)

// groupProject 结构：共享上游供给三个分支，分别发布给接收方 财务 / 运营 / 风控。
func groupProject() Project {
	return Project{
		Format: "dataflow.project/v1", ID: "grp", Name: "三接收方发布组",
		Nodes: []Node{
			{ID: "shared", Type: "file_input", Name: "共享上游", Params: map[string]any{"fileName": "s.csv", "format": "csv", "content": "id,city,amount\n1,S,10\n2,B,20"}},
			{ID: "fin", Type: "transform", Name: "财务口径", Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "S"}},
			{ID: "ops", Type: "transform", Name: "运营口径", Params: map[string]any{"operation": "passthrough"}},
			{ID: "risk", Type: "transform", Name: "风控口径", Params: map[string]any{"operation": "sort", "field": "id"}},
			{ID: "pub-fin", Type: "publish", Name: "发布财务", Params: map[string]any{"receiver": "财务"}},
			{ID: "pub-ops", Type: "publish", Name: "发布运营", Params: map[string]any{"receiver": "运营"}},
			{ID: "pub-risk", Type: "publish", Name: "发布风控", Params: map[string]any{"receiver": "风控"}},
		},
		Edges: []Edge{
			{ID: "s-fin", Source: "shared", Target: "fin", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-ops", Source: "shared", Target: "ops", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-risk", Source: "shared", Target: "risk", SourcePort: "rows", TargetPort: "rows"},
			{ID: "fin-pub", Source: "fin", Target: "pub-fin", SourcePort: "rows", TargetPort: "rows"},
			{ID: "ops-pub", Source: "ops", Target: "pub-ops", SourcePort: "rows", TargetPort: "rows"},
			{ID: "risk-pub", Source: "risk", Target: "pub-risk", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func membersOf(deliveries map[string]DeliveryState, nodeIDs ...string) []PublishGroupMember {
	members := make([]PublishGroupMember, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		pending := deliveries[nodeID].Pending
		if pending == nil {
			panic("no pending for " + nodeID)
		}
		members = append(members, PublishGroupMember{NodeID: nodeID, Version: pending.Version, RunID: pending.RunID})
	}
	return members
}

func runGroupProject(t *testing.T) (Project, RunResult) {
	t.Helper()
	project := groupProject()
	result := Run(project, "run-1", nil, nil, nil, t.TempDir())
	if !result.OK {
		t.Fatalf("run failed: %#v", result.Issues)
	}
	return project, result
}

// 三接收方循环依赖：A 依赖 B、B 依赖 C、C 依赖 A，版本一致时整组共同生效。
func TestGroupCommitWithCyclicDependencies(t *testing.T) {
	project, result := runGroupProject(t)
	deliveries := result.Deliveries
	groups := map[string]PublishGroup{}
	archives := archiveMapFrom(result.CandidateArchives)
	members := membersOf(deliveries, "pub-fin", "pub-ops", "pub-risk")
	version := func(nodeID string) string { return deliveries[nodeID].Pending.Version }
	dependencies := []PublishGroupDependency{
		{NodeID: "pub-fin", DependsOn: "pub-ops", Version: version("pub-ops")},
		{NodeID: "pub-ops", DependsOn: "pub-risk", Version: version("pub-risk")},
		{NodeID: "pub-risk", DependsOn: "pub-fin", Version: version("pub-fin")},
	}
	group, err := PrepareGroup(deliveries, "grp-1", members, dependencies, ProjectVersion(project))
	if err != nil {
		t.Fatalf("cyclic dependencies inside one group must be allowed: %v", err)
	}
	groups[group.ID] = group
	if group.Basis["pub-fin"] != "" || group.Basis["pub-ops"] != "" || group.Basis["pub-risk"] != "" {
		t.Fatalf("首次发布前各接收方可见版本应为空: %#v", group.Basis)
	}
	deliveries, committed, err := CommitGroup(deliveries, groups, archives, group.ID, nil, ProjectVersion(project))
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if committed.Status != GroupStatusCommitted {
		t.Fatalf("status = %s, want committed", committed.Status)
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-risk"} {
		state := deliveries[nodeID]
		if state.Published == nil || state.Pending != nil {
			t.Fatalf("%s 应对接收方可见且无遗留待确认: %#v", nodeID, state)
		}
		adoption := committed.Adopted[nodeID]
		if adoption.Version != state.Published.Version || adoption.RunID != state.Published.RunID {
			t.Fatalf("%s 实际采用身份记录错误: %#v vs %#v", nodeID, adoption, state.Published)
		}
	}
	// 重复提交幂等：不重复发布、不重复记录。
	before := len(deliveries["pub-fin"].History)
	if _, again, err := CommitGroup(deliveries, groups, archives, group.ID, nil, ProjectVersion(project)); err != nil || again.Status != GroupStatusCommitted {
		t.Fatalf("retry must be idempotent: %v %s", err, again.Status)
	}
	if len(deliveries["pub-fin"].History) != before {
		t.Fatal("重复提交不得追加发布证据")
	}
}

// 依赖版本不一致时整组不成立。
func TestGroupPrepareRejectsInconsistentDependency(t *testing.T) {
	project, result := runGroupProject(t)
	members := membersOf(result.Deliveries, "pub-fin", "pub-ops")
	dependencies := []PublishGroupDependency{{NodeID: "pub-fin", DependsOn: "pub-ops", Version: "v-not-exist"}}
	if _, err := PrepareGroup(result.Deliveries, "grp-x", members, dependencies, ProjectVersion(project)); err == nil || !strings.Contains(err.Error(), "依赖版本不一致") {
		t.Fatalf("inconsistent dependency must be rejected, got %v", err)
	}
}

// 过期候选混入：组内任一候选失效即整组拒绝，且没有任何接收方被发布。
func TestGroupRejectsStaleCandidateMixedIn(t *testing.T) {
	project, result := runGroupProject(t)
	deliveries := result.Deliveries
	members := membersOf(deliveries, "pub-fin", "pub-ops", "pub-risk")
	// 预备时混入已失效候选：直接拒绝。
	deliveries = invalidatePendingDelivery(deliveries, "pub-ops", "本轮运行中该分支执行失败，待确认结果失去运行依据")
	if _, err := PrepareGroup(deliveries, "grp-stale", members, nil, ProjectVersion(project)); err == nil || !strings.Contains(err.Error(), "失效") {
		t.Fatalf("stale candidate must fail prepare, got %v", err)
	}
	// 预备后候选才失效（工程被修改）：提交时共同校验拒绝，整组保持未发布。
	fresh := Run(project, "run-2", nil, nil, nil, t.TempDir())
	if !fresh.OK {
		t.Fatalf("fresh run failed: %#v", fresh.Issues)
	}
	deliveries2 := fresh.Deliveries
	members = membersOf(deliveries2, "pub-fin", "pub-ops", "pub-risk")
	group, err := PrepareGroup(deliveries2, "grp-stale-2", members, nil, ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]PublishGroup{group.ID: group}
	deliveries2 = invalidatePendingDelivery(deliveries2, "pub-risk", "候选计算后工程已被修改")
	if _, _, err := CommitGroup(deliveries2, groups, map[string]CandidateArchive{}, group.ID, nil, ProjectVersion(project)); err == nil {
		t.Fatal("commit with stale member must be rejected")
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-risk"} {
		if deliveries2[nodeID].Published != nil {
			t.Fatalf("任一条件不满足时整组保持未发布，但 %s 被发布了", nodeID)
		}
	}
}

// 两组交叉争用同一接收方：以接收方当前可见版本为共同前提，
// 即使候选输出内容相同，也不能绕过依据变化或覆盖别组的新版本。
func TestGroupConflictUsesReceiverVisibleVersion(t *testing.T) {
	project, result := runGroupProject(t)
	deliveries := result.Deliveries
	groups := map[string]PublishGroup{}
	archives := archiveMapFrom(result.CandidateArchives)
	members := membersOf(deliveries, "pub-fin", "pub-ops")
	group, err := PrepareGroup(deliveries, "grp-c", members, nil, ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	groups[group.ID] = group
	// 模拟别组抢先发布：接收方 财务 当前可见版本从空变为别组版本，
	// 且该版本与本组候选输出内容完全相同（同内容也不能绕过依据变化）。
	state := deliveries["pub-fin"]
	rushed := *state.Pending
	rushed.RunID = "run-other-group"
	state.Published = &rushed
	deliveries["pub-fin"] = state
	if _, _, err := CommitGroup(deliveries, groups, archives, group.ID, nil, ProjectVersion(project)); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("visible-version drift must be a conflict even with identical content, got %v", err)
	}
	if deliveries["pub-fin"].Published.RunID != "run-other-group" {
		t.Fatal("冲突时不得覆盖别组的新版本")
	}
	if deliveries["pub-ops"].Published != nil {
		t.Fatal("冲突时整组保持未发布")
	}
}

// 提交期间接收方暂时不可用：整组停留在提交中且没有任何接收方被发布；
// 恢复后到达确定结局，重试幂等。
func TestGroupOutageDuringCommitThenRecover(t *testing.T) {
	project, result := runGroupProject(t)
	deliveries := result.Deliveries
	groups := map[string]PublishGroup{}
	archives := archiveMapFrom(result.CandidateArchives)
	members := membersOf(deliveries, "pub-fin", "pub-ops", "pub-risk")
	group, err := PrepareGroup(deliveries, "grp-o", members, nil, ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	groups[group.ID] = group
	// 接收方 运营 暂时不可用：整组不发布任何成员。
	if _, failing, err := CommitGroup(deliveries, groups, archives, group.ID, map[string]bool{"pub-ops": true}, ProjectVersion(project)); err == nil {
		t.Fatal("unavailable receiver must block the whole group")
	} else if failing.Status != GroupStatusCommitting {
		t.Fatalf("status = %s, want committing", failing.Status)
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-risk"} {
		if deliveries[nodeID].Published != nil {
			t.Fatalf("读取者只能看到完整旧组或完整新组，但 %s 被部分发布", nodeID)
		}
	}
	// 已经进入提交的组不能取消。
	if _, err := CancelGroup(groups, group.ID); err == nil {
		t.Fatal("committing group must not be cancellable")
	}
	// 恢复：到达确定结局，记录每个接收方实际采用的候选身份。
	deliveries, recovered, err := RecoverGroup(deliveries, groups, archives, group.ID, ProjectVersion(project))
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	if recovered.Status != GroupStatusCommitted || len(recovered.Adopted) != 3 {
		t.Fatalf("recover must reach a definite outcome: %#v", recovered)
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-risk"} {
		if deliveries[nodeID].Published == nil {
			t.Fatalf("恢复后 %s 应对接收方可见", nodeID)
		}
	}
	// 页面刷新式的重试：幂等，不重复发布。
	events := len(deliveries["pub-ops"].History)
	if _, _, err := CommitGroup(deliveries, groups, archives, group.ID, nil, ProjectVersion(project)); err != nil {
		t.Fatal(err)
	}
	if len(deliveries["pub-ops"].History) != events {
		t.Fatal("刷新重试不得导致重复发布")
	}
}

// 尚未进入提交阶段的组可以整组取消，候选保持待确认。
func TestCancelPreparedGroup(t *testing.T) {
	project, result := runGroupProject(t)
	deliveries := result.Deliveries
	members := membersOf(deliveries, "pub-fin", "pub-ops")
	group, err := PrepareGroup(deliveries, "grp-cancel", members, nil, ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]PublishGroup{group.ID: group}
	cancelled, err := CancelGroup(groups, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != GroupStatusCancelled {
		t.Fatalf("status = %s, want cancelled", cancelled.Status)
	}
	if deliveries["pub-fin"].Pending == nil || deliveries["pub-fin"].Published != nil {
		t.Fatal("取消只影响组，不影响待确认候选本身")
	}
	if _, _, err := CommitGroup(deliveries, groups, map[string]CandidateArchive{}, group.ID, nil, ProjectVersion(project)); err == nil {
		t.Fatal("已取消的组不能提交")
	}
}
