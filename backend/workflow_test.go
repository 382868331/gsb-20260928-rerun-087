package main

import "testing"

func testProject() Project {
	return Project{
		ID: "p1", Name: "test", Version: 1,
		Nodes: []Node{
			{ID: "in", Type: "file-input", Name: "in", Params: map[string]string{"content": "a\nb"}},
			{ID: "tx", Type: "transform", Name: "tx", Params: map[string]string{"operation": "uppercase"}},
			{ID: "out", Type: "file-output", Name: "out"},
		},
		Edges: []Edge{
			{ID: "e1", Source: "in", Target: "tx", Kind: "data"},
			{ID: "e2", Source: "tx", Target: "out", Kind: "data"},
		},
	}
}

func TestRunInputTransformOutput(t *testing.T) {
	store := NewStore(t.TempDir() + "/db.json")
	store.Projects["p1"] = testProject()
	run, err := store.StartRun("p1", "")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" {
		t.Fatalf("status=%s error=%v", run.Status, run.NodeStates["out"].Error)
	}
	if got := run.Artifacts["out"]; got != "A\nB" {
		t.Fatalf("artifact=%q", got)
	}
}

func TestRejectsCycleBeforeStart(t *testing.T) {
	p := testProject()
	p.Edges = append(p.Edges, Edge{ID: "cycle", Source: "out", Target: "in", Kind: "data"})
	if err := ValidateProject(p); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestFailureInvalidatesDescendantsWithoutReusingOldArtifact(t *testing.T) {
	store := NewStore(t.TempDir() + "/db.json")
	store.Projects["p1"] = testProject()
	if _, err := store.StartRun("p1", ""); err != nil {
		t.Fatal(err)
	}
	p := store.Projects["p1"]
	p.Nodes[0].Params["content"] = " "
	store.Projects["p1"] = p
	run, err := store.StartRun("p1", "")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" {
		t.Fatalf("status=%s", run.Status)
	}
	for _, id := range []string{"in", "tx", "out"} {
		if run.NodeStates[id].Status != "failed" {
			t.Fatalf("%s=%s", id, run.NodeStates[id].Status)
		}
		if _, ok := run.Artifacts[id]; ok {
			t.Fatalf("%s artifact should not exist", id)
		}
	}
}

func TestLocalRerunReusesUpstreamAndRecomputesScope(t *testing.T) {
	store := NewStore(t.TempDir() + "/db.json")
	store.Projects["p1"] = testProject()
	first, err := store.StartRun("p1", "")
	if err != nil {
		t.Fatal(err)
	}
	p := store.Projects["p1"]
	p.Nodes[1].Params["operation"] = "lowercase"
	store.Projects["p1"] = p
	second, err := store.StartRun("p1", "tx")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "success" {
		t.Fatal(second.Status)
	}
	if got := second.NodeStates["in"].Data[0]; got != "a" {
		t.Fatalf("upstream changed=%v", got)
	}
	if got := second.Artifacts["out"]; got != "a\nb" {
		t.Fatalf("output=%q", got)
	}
	if first.Artifacts["out"] != "A\nB" {
		t.Fatal("old run version retained")
	}
}

func TestManualBranchThenJoin(t *testing.T) {
	p := testProject()
	p.Nodes = append(p.Nodes[:2],
		Node{ID: "manual", Type: "manual", Name: "review"},
		Node{ID: "join", Type: "file-output", Name: "join"},
	)
	p.Edges = []Edge{
		{ID: "e1", Source: "in", Target: "tx", Kind: "data"},
		{ID: "e2", Source: "tx", Target: "manual", Kind: "data"},
		{ID: "e3", Source: "manual", Target: "join", Kind: "data"},
	}
	store := NewStore(t.TempDir() + "/db.json")
	store.Projects["p1"] = p
	run, err := store.StartRun("p1", "")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "waiting" {
		t.Fatalf("status=%s", run.Status)
	}
	run, err = store.Decide(run.ID, DecisionRequest{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.Artifacts["join"] != "A\nB" {
		t.Fatalf("run=%+v", run)
	}
}
