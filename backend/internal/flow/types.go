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
	NodeID  string `json:"nodeId"`
	Version string `json:"version"`
	RunID   string `json:"runId"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	Rows    []Row  `json:"rows"`
	Summary string `json:"summary"`
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
	NodeID     string    `json:"nodeId"`
	Status     string    `json:"status"`
	Artifact   *Artifact `json:"artifact,omitempty"`
	Error      string    `json:"error,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ReusedFrom string    `json:"reusedFrom,omitempty"`
	Cause      string    `json:"cause,omitempty"`
}

// Delivery 是一次面向接收方的结果投递记录：版本、来源运行与行数。
type Delivery struct {
	NodeID         string `json:"nodeId"`
	Receiver       string `json:"receiver"`
	Version        string `json:"version"`
	RunID          string `json:"runId"`
	ProjectVersion string `json:"projectVersion,omitempty"`
	Rows           int    `json:"rows"`
	Empty          bool   `json:"empty"`
	Summary        string `json:"summary"`
}

// DeliveryEvent 是追加式证据日志：已发出的结果即使撤回修订也保留。
type DeliveryEvent struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	RunID   string `json:"runId"`
	Note    string `json:"note"`
}

const (
	EventComputed    = "computed"
	EventSuperseded  = "superseded"
	EventPublished   = "published"
	EventInvalidated = "invalidated"
)

// DeliveryState 记录一个发布节点的当前投递状态：
// Pending 仅已计算（对接收方不可见），Published 已对接收方可见。
// Invalidated 是因依据（来源 / 流程修订 / 运行结果）变化而失效的候选，
// 保留供查阅失败前后差异，但不能再被确认。
type DeliveryState struct {
	NodeID      string          `json:"nodeId"`
	Receiver    string          `json:"receiver"`
	Pending     *Delivery       `json:"pending,omitempty"`
	Published   *Delivery       `json:"published,omitempty"`
	Invalidated *Delivery       `json:"invalidated,omitempty"`
	History     []DeliveryEvent `json:"history"`
}

const (
	CauseReused      = "reused"
	CauseRerunFull   = "rerun_full"
	CauseRerunParams = "rerun_params"
	CauseRerunInput  = "rerun_input"
	CauseRerunForced = "rerun_forced"
	CauseRerunFirst  = "rerun_first"
)

type RunResult struct {
	OK              bool                     `json:"ok"`
	RunID           string                   `json:"runId"`
	ProjectVersion  string                   `json:"projectVersion"`
	AffectedNodeIDs []string                 `json:"affectedNodeIds"`
	Results         map[string]NodeResult    `json:"results"`
	Deliveries      map[string]DeliveryState `json:"deliveries,omitempty"`
	Issues          []Issue                  `json:"issues,omitempty"`
}
