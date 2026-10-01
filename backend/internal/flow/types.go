package flow

type Project struct {
	Format   string `json:"format"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int    `json:"revision"`
	Nodes    []Node `json:"nodes"`
	Edges    []Edge `json:"edges"`
}

type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Position Position       `json:"position"`
	Params   map[string]any `json:"params"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Edge struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	SourcePort string `json:"sourcePort"`
	TargetPort string `json:"targetPort"`
}

type Component struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Inputs      []Port  `json:"inputs"`
	Outputs     []Port  `json:"outputs"`
	Params      []Field `json:"params"`
}

type Port struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Multi bool   `json:"multi"`
}

type Field struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description"`
	Options     []string `json:"options,omitempty"`
}

type Artifact struct {
	NodeID        string            `json:"nodeId"`
	Version       string            `json:"version"`
	ParamsVersion string            `json:"paramsVersion"`
	InputVersions map[string]string `json:"inputVersions,omitempty"`
	RunID         string            `json:"runId"`
	Path          string            `json:"path,omitempty"`
	Content       string            `json:"content,omitempty"`
	Rows          []Row             `json:"rows"`
	Summary       string            `json:"summary"`
}

type Row map[string]any

type RunRequest struct {
	Project        Project             `json:"project"`
	StartNodeIDs   []string            `json:"startNodeIds"`
	InputArtifacts map[string]Artifact `json:"inputArtifacts"`
}

type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
	EdgeID  string `json:"edgeId,omitempty"`
}

type ValidationResult struct {
	OK              bool     `json:"ok"`
	Issues          []Issue  `json:"issues"`
	ExecutionOrder  []string `json:"executionOrder"`
	AffectedNodeIDs []string `json:"affectedNodeIds"`
}

type NodeResult struct {
	NodeID      string    `json:"nodeId"`
	Status      string    `json:"status"`
	Artifact    *Artifact `json:"artifact,omitempty"`
	Error       string    `json:"error,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	ReusedFrom  string    `json:"reusedFrom,omitempty"`
	ChangeBasis string    `json:"changeBasis,omitempty"`
	PrevVersion string    `json:"prevVersion,omitempty"`
}

type RunResult struct {
	OK              bool                  `json:"ok"`
	RunID           string                `json:"runId"`
	ProjectVersion  string                `json:"projectVersion"`
	AffectedNodeIDs []string              `json:"affectedNodeIds"`
	Results         map[string]NodeResult `json:"results"`
	Issues          []Issue               `json:"issues,omitempty"`
}
