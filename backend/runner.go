package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

func (s *Store) StartRun(projectID, fromNodeID string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Projects[projectID]
	if !ok {
		return Run{}, fmt.Errorf("project %s not found", projectID)
	}
	if err := ValidateProject(p); err != nil {
		return Run{}, err
	}
	run := Run{
		ID: newID("run"), ProjectID: projectID, ProjectVersion: p.Version,
		FromNodeID: fromNodeID, Status: "running", NodeStates: map[string]NodeState{}, Artifacts: map[string]string{},
	}
	scope := map[string]bool{}
	for _, n := range p.Nodes {
		scope[n.ID] = true
		run.NodeStates[n.ID] = NodeState{ID: n.ID, Status: "stale"}
	}
	if fromNodeID != "" {
		if nodeByID(p, fromNodeID).ID == "" {
			return Run{}, fmt.Errorf("rerun node %s not found", fromNodeID)
		}
		previous := s.latestRunLocked(projectID)
		if previous.ID == "" {
			return Run{}, fmt.Errorf("cannot rerun %s before a successful base run", fromNodeID)
		}
		scope = descendants(p, fromNodeID)
		for _, n := range p.Nodes {
			if !scope[n.ID] {
				st := previous.NodeStates[n.ID]
				if st.Status != "success" {
					return Run{}, fmt.Errorf("upstream node %s is not successful", n.ID)
				}
				run.NodeStates[n.ID] = st
				if a, ok := previous.Artifacts[n.ID]; ok {
					run.Artifacts[n.ID] = a
				}
			}
		}
	}
	s.Runs[run.ID] = run
	s.advanceLocked(p, &run, scope, false)
	s.Runs[run.ID] = run
	if err := s.SaveLocked(); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *Store) Decide(runID string, req DecisionRequest) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.Runs[runID]
	if !ok {
		return Run{}, fmt.Errorf("run %s not found", runID)
	}
	if run.Status != "waiting" {
		return Run{}, fmt.Errorf("run %s is not waiting", runID)
	}
	p := s.Projects[run.ProjectID]
	waiting := ""
	for id, st := range run.NodeStates {
		if st.Status == "waiting" {
			waiting = id
		}
	}
	if waiting == "" {
		return Run{}, fmt.Errorf("no waiting manual step")
	}
	if !req.Approved {
		run.NodeStates[waiting] = NodeState{ID: waiting, Status: "failed", Error: "manual branch rejected: " + req.Comment, Attempt: 1}
		s.failDescendantsLocked(p, &run, waiting)
		run.Status = "failed"
	} else {
		rows := flattenRows(s.readyDataLocked(p, run, waiting))
		run.NodeStates[waiting] = successState(waiting, rows)
		run.Artifacts[waiting] = strings.Join(rows, "\n")
		scope := map[string]bool{}
		for id := range run.NodeStates {
			scope[id] = true
		}
		s.advanceLocked(p, &run, scope, true)
	}
	s.Runs[runID] = run
	if err := s.SaveLocked(); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *Store) latestRunLocked(projectID string) Run {
	var latest Run
	for _, r := range s.Runs {
		if r.ProjectID == projectID && (latest.ID == "" || r.ProjectVersion > latest.ProjectVersion || (r.ProjectVersion == latest.ProjectVersion && r.ID > latest.ID)) {
			latest = r
		}
	}
	return latest
}

func (s *Store) advanceLocked(p Project, run *Run, scope map[string]bool, resume bool) {
	if resume {
		run.Status = "running"
	}
	for {
		order, _ := topoOrder(p)
		progressed := false
		for _, id := range order {
			if !scope[id] || run.NodeStates[id].Status != "stale" {
				continue
			}
			n := nodeByID(p, id)
			edges := incomingEdges(p, id)
			depStatus := "ready"
			for _, e := range edges {
				st := run.NodeStates[e.Source].Status
				if st == "failed" || (st == "stale" && !scope[e.Source]) {
					depStatus = "blocked"
					break
				}
				if st == "waiting" || st == "stale" {
					depStatus = "wait"
					break
				}
			}
			if depStatus == "blocked" {
				run.NodeStates[id] = NodeState{ID: id, Status: "failed", Error: "skipped because upstream has no valid result this run"}
				s.failDescendantsLocked(p, run, id)
				run.Status = "failed"
				return
			}
			if depStatus == "wait" {
				continue
			}
			if n.Type == "manual" {
				rows := flattenRows(s.readyDataLocked(p, *run, id))
				run.NodeStates[id] = NodeState{ID: id, Status: "waiting", Data: rows, Preview: preview(rows), Attempt: run.NodeStates[id].Attempt + 1}
				run.Status = "waiting"
				return
			}
			params := map[string]string{}
			for k, v := range n.Params {
				params[k] = v
			}
			for _, e := range edges {
				if e.Kind == "parameter" {
					params[e.Parameter] = strings.Join(run.NodeStates[e.Source].Data, "\n")
				}
			}
			rows, err := executeNode(n, s.readyDataLocked(p, *run, id), params)
			if err != nil {
				run.NodeStates[id] = NodeState{ID: id, Status: "failed", Error: err.Error(), Attempt: run.NodeStates[id].Attempt + 1}
				s.failDescendantsLocked(p, run, id)
				run.Status = "failed"
				return
			}
			run.NodeStates[id] = successState(id, rows)
			run.Artifacts[id] = strings.Join(rows, "\n")
			progressed = true
		}
		if !progressed {
			break
		}
	}
	if run.Status == "running" {
		run.Status = "success"
	}
}

func (s *Store) failDescendantsLocked(p Project, run *Run, failed string) {
	for id := range descendants(p, failed) {
		if id == failed {
			continue
		}
		if run.NodeStates[id].Status != "success" && run.NodeStates[id].Status != "waiting" {
			run.NodeStates[id] = NodeState{ID: id, Status: "failed", Error: "invalidated: upstream " + failed + " did not produce a current result"}
		}
	}
}

func (s *Store) readyDataLocked(p Project, run Run, id string) [][]string {
	out := [][]string{}
	for _, e := range incomingEdges(p, id) {
		if e.Kind == "data" && run.NodeStates[e.Source].Status == "success" {
			out = append(out, run.NodeStates[e.Source].Data)
		}
	}
	return out
}

func incomingEdges(p Project, target string) []Edge {
	out := []Edge{}
	for _, e := range p.Edges {
		if e.Target == target {
			out = append(out, e)
		}
	}
	return out
}

func flattenRows(batches [][]string) []string {
	rows := []string{}
	for _, batch := range batches {
		rows = append(rows, batch...)
	}
	return rows
}

func successState(id string, rows []string) NodeState {
	return NodeState{ID: id, Status: "success", Data: rows, Preview: preview(rows), Attempt: 1}
}

func newID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
