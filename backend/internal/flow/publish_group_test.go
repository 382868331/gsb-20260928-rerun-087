package flow

import (
	"strings"
	"testing"
)

// threeReceiverProject 结构：共享上游供给三个分支，分别发布给 财务 / 运营 / 人事。
func threeReceiverProject() Project {
	return Project{
		Format: "dataflow.project/v1", ID: "pg", Name: "三接收方发布组",
		Nodes: []Node{
			{ID: "shared", Type: "file_input", Name: "共享上游", Params: map[string]any{"fileName": "s.csv", "format": "csv", "content": "id,city,amount\n1,S,10\n2,B,20"}},
			{ID: "fin", Type: "transform", Name: "财务口径", Params: map[string]any{"operation": "passthrough"}},
			{ID: "ops", Type: "transform", Name: "运营口径", Params: map[string]any{"operation": "passthrough"}},
			{ID: "hr", Type: "transform", Name: "人事口径", Params: map[string]any{"operation": "passthrough"}},
			{ID: "pub-fin", Type: "publish", Name: "发布财务", Params: map[string]any{"receiver": "财务"}},
			{ID: "pub-ops", Type: "publish", Name: "发布运营", Params: map[string]any{"receiver": "运营"}},
			{ID: "pub-hr", Type: "publish", Name: "发布人事", Params: map[string]any{"receiver": "人事"}},
		},
		Edges: []Edge{
			{ID: "s-fin", Source: "shared", Target: "fin", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-ops", Source: "shared", Target: "ops", SourcePort: "rows", TargetPort: "rows"},
			{ID: "s-hr", Source: "shared", Target: "hr", SourcePort: "rows", TargetPort: "rows"},
			{ID: "fin-pub", Source: "fin", Target: "pub-fin", SourcePort: "rows", TargetPort: "rows"},
			{ID: "ops-pub", Source: "ops", Target: "pub-ops", SourcePort: "rows", TargetPort: "rows"},
			{ID: "hr-pub", Source: "hr", Target: "pub-hr", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func runThree(t *testing.T) (Project, RunResult) {
	t.Helper()
	project := threeReceiverProject()
	result := Run(project, "run-1", nil, nil, nil, t.TempDir())
	if !result.OK {
		t.Fatalf("run failed: %#v", result.Issues)
	}
	return project, result
}

func TestPublishGroupRejectsCyclicDependencies(t *testing.T) {
	project, result := runThree(t)
	deps := []PublishGroupDependency{
		{Receiver: "财务", OnReceiver: "运营", Version: result.Deliveries["pub-ops"].Pending.Version},
		{Receiver: "运营", OnReceiver: "人事", Version: result.Deliveries["pub-hr"].Pending.Version},
		{Receiver: "人事", OnReceiver: "财务", Version: result.Deliveries["pub-fin"].Pending.Version},
	}
	_, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops", "pub-hr"}, deps, "pg-1", "run-1", ProjectVersion(project))
	if err == nil || !strings.Contains(err.Error(), "循环") {
		t.Fatalf("三接收方循环依赖必须被拒绝, got %v", err)
	}
}

func TestPublishGroupRejectsInconsistentDependencyVersion(t *testing.T) {
	project, result := runThree(t)
	deps := []PublishGroupDependency{{Receiver: "财务", OnReceiver: "运营", Version: "v-not-real"}}
	_, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops"}, deps, "pg-1", "run-1", ProjectVersion(project))
	if err == nil || !strings.Contains(err.Error(), "依赖版本不一致") {
		t.Fatalf("依赖版本不一致必须被拒绝, got %v", err)
	}
}

func TestPublishGroupCommitAllOrNothing(t *testing.T) {
	project, result := runThree(t)
	group, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops", "pub-hr"}, nil, "pg-1", "run-1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	archives := archiveMapFrom(result.CandidateArchives)
	updated, err := ConfirmPublishGroup(result.Deliveries, archives, group, nil, ProjectVersion(project), "run-c1")
	if err != nil {
		t.Fatal(err)
	}
	if group.Status != GroupCommitted {
		t.Fatalf("group status = %s, want committed", group.Status)
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-hr"} {
		if updated[nodeID].Published == nil || updated[nodeID].Pending != nil {
			t.Fatalf("%s 应整组对接收方可见: %#v", nodeID, updated[nodeID])
		}
	}
	if len(group.Outcomes) != 3 {
		t.Fatalf("应记录 3 个接收方实际采用的候选, got %d", len(group.Outcomes))
	}
	// 幂等：重复确认 / 页面刷新不重复发布、不重复记录。
	again, err := ConfirmPublishGroup(updated, archives, group, nil, ProjectVersion(project), "run-c2")
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-hr"} {
		publishedEvents := 0
		for _, event := range again[nodeID].History {
			if event.Kind == EventPublished {
				publishedEvents++
			}
		}
		if publishedEvents != 1 {
			t.Fatalf("%s 重复确认后 published 事件 = %d, want 1", nodeID, publishedEvents)
		}
	}
}

func TestPublishGroupConflictUsesVisibleVersionPrecondition(t *testing.T) {
	project, result := runThree(t)
	group, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops"}, nil, "pg-1", "run-1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	// 另一组先把新版本发布给 财务：可见版本偏离建组时的共同前提，
	// 但本组候选仍在（即使输出内容相同也不能绕过前提判定）。
	state := result.Deliveries["pub-fin"]
	state.Published = &Delivery{NodeID: "pub-fin", Receiver: "财务", Version: "v-other-group", RunID: "run-other"}
	result.Deliveries["pub-fin"] = state
	archives := archiveMapFrom(result.CandidateArchives)
	after, err := ConfirmPublishGroup(result.Deliveries, archives, group, nil, ProjectVersion(project), "run-c1")
	if err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("可见版本变化必须判定冲突, got %v", err)
	}
	if group.Status != GroupAborted {
		t.Fatalf("冲突后整组应中止, got %s", group.Status)
	}
	if after["pub-ops"].Published != nil {
		t.Fatal("任一条件不满足时整组保持未发布：运营不应被发布")
	}
}

func TestPublishGroupCommitInterruptedThenRecovers(t *testing.T) {
	project, result := runThree(t)
	group, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops", "pub-hr"}, nil, "pg-1", "run-1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	archives := archiveMapFrom(result.CandidateArchives)
	// 提交期间 运营 接收方暂时不可用：组停留在提交中。
	down := map[string]bool{"运营": true}
	updated, err := ConfirmPublishGroup(result.Deliveries, archives, group, down, ProjectVersion(project), "run-c1")
	if err == nil || !strings.Contains(err.Error(), "暂时不可用") {
		t.Fatalf("接收方不可用应返回可恢复错误, got %v", err)
	}
	if group.Status != GroupCommitting {
		t.Fatalf("组应停留在提交中, got %s", group.Status)
	}
	if len(group.Outcomes) != 2 {
		t.Fatalf("不可用前已完成 2 个接收方, got %d", len(group.Outcomes))
	}
	if updated["pub-ops"].Published != nil {
		t.Fatal("不可用接收方不应出现新版本")
	}
	// 读取者只能看到完整旧组或完整新组：提交中取消必须被拒绝。
	if cancelErr := CancelPublishGroup(group); cancelErr == nil {
		t.Fatal("提交中的组不能取消")
	}
	// 恢复可用后重试：组进入确定结局，已完成接收方不重复发布。
	recovered, err := ConfirmPublishGroup(updated, archives, group, nil, ProjectVersion(project), "run-c2")
	if err != nil {
		t.Fatal(err)
	}
	if group.Status != GroupCommitted {
		t.Fatalf("恢复后应已完成, got %s", group.Status)
	}
	if recovered["pub-ops"].Published == nil {
		t.Fatal("恢复后运营应对接收方可见")
	}
	for _, nodeID := range []string{"pub-fin", "pub-hr"} {
		publishedEvents := 0
		for _, event := range recovered[nodeID].History {
			if event.Kind == EventPublished {
				publishedEvents++
			}
		}
		if publishedEvents != 1 {
			t.Fatalf("%s 重试不得重复发布, published 事件 = %d", nodeID, publishedEvents)
		}
	}
	// 每个接收方实际采用的候选身份留痕。
	adopted := map[string]string{}
	for _, outcome := range group.Outcomes {
		adopted[outcome.Receiver] = outcome.Version
	}
	for _, member := range group.Members {
		if adopted[member.Receiver] != member.Version {
			t.Fatalf("接收方 %s 采用候选 %s, want %s", member.Receiver, adopted[member.Receiver], member.Version)
		}
	}
}

func TestPublishGroupRejectsStaleMember(t *testing.T) {
	project, result := runThree(t)
	group, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops", "pub-hr"}, nil, "pg-1", "run-1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	// 过期候选混入：人事候选失去依据。
	state := result.Deliveries["pub-hr"]
	stale := *state.Pending
	stale.Stale = true
	stale.StaleReason = "来源运行失败，候选失去运行依据"
	state.Pending = &stale
	result.Deliveries["pub-hr"] = state
	archives := archiveMapFrom(result.CandidateArchives)
	updated, err := ConfirmPublishGroup(result.Deliveries, archives, group, nil, ProjectVersion(project), "run-c1")
	if err == nil || !strings.Contains(err.Error(), "已失效") {
		t.Fatalf("混入失效候选必须整组拒绝, got %v", err)
	}
	if group.Status != GroupAborted {
		t.Fatalf("整组应中止, got %s", group.Status)
	}
	for _, nodeID := range []string{"pub-fin", "pub-ops", "pub-hr"} {
		if updated[nodeID].Published != nil {
			t.Fatalf("%s 不应被部分发布", nodeID)
		}
	}
}

func TestPublishGroupCancelOnlyBeforeCommit(t *testing.T) {
	project, result := runThree(t)
	group, err := CreatePublishGroup(result.Deliveries, []string{"pub-fin", "pub-ops"}, nil, "pg-1", "run-1", ProjectVersion(project))
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelPublishGroup(group); err != nil {
		t.Fatal(err)
	}
	if group.Status != GroupCancelled {
		t.Fatalf("status = %s, want cancelled", group.Status)
	}
	// 取消后不能确认。
	if _, err := ConfirmPublishGroup(result.Deliveries, nil, group, nil, ProjectVersion(project), "run-c1"); err == nil {
		t.Fatal("已取消的组不能确认")
	}
}
