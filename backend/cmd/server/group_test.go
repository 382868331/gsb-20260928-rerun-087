package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"dataflow-designer/internal/flow"
)

func groupTestProject() flow.Project {
	return flow.Project{
		Format: "dataflow.project/v1", ID: "grp-http", Name: "发布组测试",
		Nodes: []flow.Node{
			{ID: "in", Type: "file_input", Name: "输入", Params: map[string]any{"fileName": "a.csv", "format": "csv", "content": "id,city\n1,S\n2,B"}},
			{ID: "t1", Type: "transform", Name: "财务口径", Params: map[string]any{"operation": "filter_equals", "field": "city", "value": "S"}},
			{ID: "t2", Type: "transform", Name: "运营口径", Params: map[string]any{"operation": "passthrough"}},
			{ID: "t3", Type: "transform", Name: "风控口径", Params: map[string]any{"operation": "sort", "field": "id"}},
			{ID: "pub-a", Type: "publish", Name: "发布A", Params: map[string]any{"receiver": "财务"}},
			{ID: "pub-b", Type: "publish", Name: "发布B", Params: map[string]any{"receiver": "运营"}},
			{ID: "pub-c", Type: "publish", Name: "发布C", Params: map[string]any{"receiver": "风控"}},
		},
		Edges: []flow.Edge{
			{ID: "i1", Source: "in", Target: "t1", SourcePort: "rows", TargetPort: "rows"},
			{ID: "i2", Source: "in", Target: "t2", SourcePort: "rows", TargetPort: "rows"},
			{ID: "i3", Source: "in", Target: "t3", SourcePort: "rows", TargetPort: "rows"},
			{ID: "p1", Source: "t1", Target: "pub-a", SourcePort: "rows", TargetPort: "rows"},
			{ID: "p2", Source: "t2", Target: "pub-b", SourcePort: "rows", TargetPort: "rows"},
			{ID: "p3", Source: "t3", Target: "pub-c", SourcePort: "rows", TargetPort: "rows"},
		},
	}
}

func prepareGroup(t *testing.T, handler http.Handler, id string, body string) flow.PublishGroup {
	t.Helper()
	response := postJSON(handler, "/api/projects/"+id+"/groups", body)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Group flow.PublishGroup `json:"group"`
	}
	json.NewDecoder(response.Body).Decode(&envelope)
	return envelope.Group
}

func groupMembers(run flow.RunResult, nodeIDs ...string) []flow.PublishGroupMember {
	members := make([]flow.PublishGroupMember, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		pending := run.Deliveries[nodeID].Pending
		members = append(members, flow.PublishGroupMember{NodeID: nodeID, Version: pending.Version, RunID: pending.RunID})
	}
	return members
}

func marshalBody(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// 页面操作链路：三接收方循环依赖 → 提交期间接收方不可用 → 恢复 → 重试幂等 →
// 两组交叉争用 → 过期候选混入。
func TestPublishGroupLifecycleOverHTTP(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	saveProject(t, handler, groupTestProject())
	run := runProject(t, handler, "grp-http")

	// 三接收方循环依赖：A→B、B→C、C→A，版本一致，整组共同生效。
	version := func(nodeID string) string { return run.Deliveries[nodeID].Pending.Version }
	members := groupMembers(run, "pub-a", "pub-b", "pub-c")
	dependencies := []flow.PublishGroupDependency{
		{NodeID: "pub-a", DependsOn: "pub-b", Version: version("pub-b")},
		{NodeID: "pub-b", DependsOn: "pub-c", Version: version("pub-c")},
		{NodeID: "pub-c", DependsOn: "pub-a", Version: version("pub-a")},
	}
	group := prepareGroup(t, handler, "grp-http", marshalBody(map[string]any{"members": members, "dependencies": dependencies}))
	if group.Status != flow.GroupStatusPrepared {
		t.Fatalf("group status = %s, want prepared", group.Status)
	}

	// 提交期间接收方 运营 暂时不可用：整组停留在提交中，任何接收方都看不到部分结果。
	response := postJSON(handler, "/api/projects/grp-http/groups/"+group.ID+"/commit", `{"unavailableReceivers":["pub-b"]}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage commit status = %d, body %s", response.Code, response.Body.String())
	}
	var outage struct {
		Group      flow.PublishGroup             `json:"group"`
		Deliveries map[string]flow.DeliveryState `json:"deliveries"`
	}
	json.NewDecoder(response.Body).Decode(&outage)
	if outage.Group.Status != flow.GroupStatusCommitting {
		t.Fatalf("outage group status = %s, want committing", outage.Group.Status)
	}
	for _, nodeID := range []string{"pub-a", "pub-b", "pub-c"} {
		if outage.Deliveries[nodeID].Published != nil {
			t.Fatalf("读取者只能看到完整旧组或完整新组，%s 不应部分可见", nodeID)
		}
	}

	// 已经进入提交的组不能取消。
	response = postJSON(handler, "/api/projects/grp-http/groups/"+group.ID+"/cancel", `{}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("committing group cancel status = %d, want 409", response.Code)
	}

	// 断开后恢复：重启（重新加载磁盘）自动把提交中的组恢复到确定结局。
	reloaded, err := newStore(filepath.Join(directory, "store.json"), filepath.Join(directory, "outputs"))
	if err != nil {
		t.Fatal(err)
	}
	recovered := reloaded.groups["grp-http"][group.ID]
	if recovered.Status != flow.GroupStatusCommitted {
		t.Fatalf("reload must recover committing group to a definite outcome, got %s", recovered.Status)
	}
	if len(recovered.Adopted) != 3 {
		t.Fatalf("必须保留每个接收方实际采用的候选身份: %#v", recovered.Adopted)
	}
	for _, nodeID := range []string{"pub-a", "pub-b", "pub-c"} {
		state := reloaded.deliveries["grp-http"][nodeID]
		if state.Published == nil || state.Published.Version != recovered.Adopted[nodeID].Version {
			t.Fatalf("恢复后 %s 可见版本与采用记录不一致: %#v", nodeID, state)
		}
	}

	// 页面刷新式的重试：幂等，不重复发布、不重复记录。
	reloadedHandler := buildMux(reloaded, "")
	eventsBefore := len(reloaded.deliveries["grp-http"]["pub-a"].History)
	response = postJSON(reloadedHandler, "/api/projects/grp-http/groups/"+group.ID+"/commit", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("retry commit status = %d, body %s", response.Code, response.Body.String())
	}
	if len(reloaded.deliveries["grp-http"]["pub-a"].History) != eventsBefore {
		t.Fatal("刷新重试不得导致重复发布")
	}
}

// 两组交叉争用同一接收方：先提交者生效，后到组以接收方当前可见版本判定冲突，整组不发布。
func TestCrossGroupContentionOverHTTP(t *testing.T) {
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	saveProject(t, handler, groupTestProject())
	run := runProject(t, handler, "grp-http")
	first := prepareGroup(t, handler, "grp-http", marshalBody(map[string]any{"members": groupMembers(run, "pub-a", "pub-b")}))
	second := prepareGroup(t, handler, "grp-http", marshalBody(map[string]any{"members": groupMembers(run, "pub-b", "pub-c")}))
	response := postJSON(handler, "/api/projects/grp-http/groups/"+first.ID+"/commit", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("first commit status = %d, body %s", response.Code, response.Body.String())
	}
	response = postJSON(handler, "/api/projects/grp-http/groups/"+second.ID+"/commit", `{}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("contending commit status = %d, want 409, body %s", response.Code, response.Body.String())
	}
	if store.deliveries["grp-http"]["pub-c"].Published != nil {
		t.Fatal("争用失败时整组保持未发布，pub-c 不应可见")
	}
	if store.deliveries["grp-http"]["pub-b"].Published == nil {
		t.Fatal("先提交的组必须保持有效")
	}
}

// 过期候选混入：任一成员失效即整组拒绝（预备与提交两道闸）。
func TestStaleCandidateMixedIntoGroupOverHTTP(t *testing.T) {
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	project := groupTestProject()
	saveProject(t, handler, project)
	run := runProject(t, handler, "grp-http")
	members := groupMembers(run, "pub-a", "pub-b", "pub-c")
	group := prepareGroup(t, handler, "grp-http", marshalBody(map[string]any{"members": members}))
	// 候选计算后工程被修改（保存）：相关待确认候选立即永久失效。
	changed := project
	changed.Nodes = append([]flow.Node(nil), project.Nodes...)
	changed.Nodes[0].Params = map[string]any{"fileName": "a.csv", "format": "csv", "content": "id,city\n9,Z\n2,B"}
	saveProject(t, handler, changed)
	response := postJSON(handler, "/api/projects/grp-http/groups/"+group.ID+"/commit", `{}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale member commit status = %d, want 409, body %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "失效") {
		t.Fatalf("拒绝原因应说明依据失效: %s", response.Body.String())
	}
	for _, nodeID := range []string{"pub-a", "pub-b", "pub-c"} {
		if store.deliveries["grp-http"][nodeID].Published != nil {
			t.Fatalf("过期候选混入时整组保持未发布，%s 不应可见", nodeID)
		}
	}
}

// 尚未进入提交阶段的组可以整组取消。
func TestCancelPreparedGroupOverHTTP(t *testing.T) {
	store, err := newStore(filepath.Join(t.TempDir(), "store.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := buildMux(store, "")
	saveProject(t, handler, groupTestProject())
	run := runProject(t, handler, "grp-http")
	group := prepareGroup(t, handler, "grp-http", marshalBody(map[string]any{"members": groupMembers(run, "pub-a", "pub-b")}))
	response := postJSON(handler, "/api/projects/grp-http/groups/"+group.ID+"/cancel", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body %s", response.Code, response.Body.String())
	}
	if store.groups["grp-http"][group.ID].Status != flow.GroupStatusCancelled {
		t.Fatalf("group status = %s, want cancelled", store.groups["grp-http"][group.ID].Status)
	}
	if store.deliveries["grp-http"]["pub-a"].Pending == nil {
		t.Fatal("取消整组不影响待确认候选本身")
	}
}
