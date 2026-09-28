package main

type Project struct {
	SchemaVersion string `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       int    `json:"version"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

type Node struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	Position Position          `json:"position"`
	Params   map[string]string `json:"params"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceHandle string `json:"sourceHandle,omitempty"`
	Target       string `json:"target"`
	TargetHandle string `json:"targetHandle,omitempty"`
	Kind         string `json:"kind"`
	Parameter    string `json:"parameter,omitempty"`
}

type Run struct {
	ID             string               `json:"id"`
	ProjectID      string               `json:"projectId"`
	ProjectVersion int                  `json:"projectVersion"`
	FromNodeID     string               `json:"fromNodeId,omitempty"`
	Status         string               `json:"status"`
	NodeStates     map[string]NodeState `json:"nodeStates"`
	Artifacts      map[string]string    `json:"artifacts"`
	Error          string               `json:"error,omitempty"`
}

type NodeState struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Data    []string `json:"data,omitempty"`
	Preview []string `json:"preview,omitempty"`
	Error   string   `json:"error,omitempty"`
	Attempt int      `json:"attempt"`
}

type SaveProjectResponse struct {
	Project Project `json:"project"`
}

type StartRunRequest struct {
	ProjectID  string `json:"projectId"`
	FromNodeID string `json:"fromNodeId,omitempty"`
}

type DecisionRequest struct {
	Approved bool   `json:"approved"`
	Comment  string `json:"comment"`
}
