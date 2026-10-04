package flow

import "time"

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
	ProjectVersion string `json:"projectVersion"`
	Rows           int    `json:"rows"`
	Empty          bool   `json:"empty"`
	Summary        string `json:"summary"`
	// Stale 标记该候选已失去依据（来源运行失败 / 流程修订变化），
	// 只能查阅不能确认；StaleReason 说明失去了哪项依据。
	Stale       bool   `json:"stale,omitempty"`
	StaleReason string `json:"staleReason,omitempty"`
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
	EventRejected    = "rejected"
)

// ExpectedCandidate 是确认方实际看过的候选版本与运行批次：
// 确认操作只对它生效，迟到的旧确认不能误发布更新后的候选。
type ExpectedCandidate struct {
	NodeID  string `json:"nodeId"`
	Version string `json:"version"`
	RunID   string `json:"runId"`
}

// DeliveryState 记录一个发布节点的当前投递状态：
// Pending 仅已计算（对接收方不可见），Published 已对接收方可见。
type DeliveryState struct {
	NodeID    string          `json:"nodeId"`
	Receiver  string          `json:"receiver"`
	Pending   *Delivery       `json:"pending,omitempty"`
	Published *Delivery       `json:"published,omitempty"`
	History   []DeliveryEvent `json:"history"`
}

// CandidateInputRevision 是候选计算时直接输入端口的不可变快照。
type CandidateInputRevision struct {
	NodeID   string `json:"nodeId"`
	NodeName string `json:"nodeName"`
	Version  string `json:"version"`
	Rows     []Row  `json:"rows"`
	RowCount int    `json:"rowCount"`
}

// CandidateArchive 是跨运行候选对比使用的只读档案。
// 即使输出记录与已有候选完全一致，也以运行批次作为独立档案保存，不能合并。
type CandidateArchive struct {
	Key             string                   `json:"key"`
	NodeID          string                   `json:"nodeId"`
	NodeName        string                   `json:"nodeName"`
	Receiver        string                   `json:"receiver"`
	RunID           string                   `json:"runId"`
	CreatedAt       time.Time                `json:"createdAt"`
	ProjectRevision int                      `json:"projectRevision"`
	ProjectVersion  string                   `json:"projectVersion"`
	OutputVersion   string                   `json:"outputVersion"`
	OutputRows      []Row                    `json:"outputRows"`
	Rows            int                      `json:"rows"`
	Empty           bool                     `json:"empty"`
	Summary         string                   `json:"summary"`
	Cause           string                   `json:"cause,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
	InputRevisions  []CandidateInputRevision `json:"inputRevisions"`
	Status          string                   `json:"status"`
}

type CandidateComparisonFilters struct {
	Receiver        string `json:"receiver"`
	ProjectRevision int    `json:"projectRevision"`
	RunID           string `json:"runId"`
	Result          string `json:"result"`
}

// CandidateComparisonState 是用户保存的只读核对现场。
type CandidateComparisonState struct {
	Filters  CandidateComparisonFilters `json:"filters"`
	LeftKey  string                     `json:"leftKey"`
	RightKey string                     `json:"rightKey"`
	OnlyDiff bool                       `json:"onlyDiff"`
}

const (
	CandidateStatusPending       = "pending"
	CandidateStatusPendingSame   = "pending_same"
	CandidateStatusPublished     = "published"
	CandidateStatusPublishedSame = "published_same"
	CandidateStatusSuperseded    = "superseded"
	CandidateStatusStale         = "stale"
)

const (
	CauseReused      = "reused"
	CauseRerunFull   = "rerun_full"
	CauseRerunParams = "rerun_params"
	CauseRerunInput  = "rerun_input"
	CauseRerunForced = "rerun_forced"
	CauseRerunFirst  = "rerun_first"
)

type RunResult struct {
	OK                bool                     `json:"ok"`
	RunID             string                   `json:"runId"`
	ProjectVersion    string                   `json:"projectVersion"`
	AffectedNodeIDs   []string                 `json:"affectedNodeIds"`
	Results           map[string]NodeResult    `json:"results"`
	Deliveries        map[string]DeliveryState `json:"deliveries,omitempty"`
	CandidateArchives []CandidateArchive       `json:"candidateArchives,omitempty"`
	Issues            []Issue                  `json:"issues,omitempty"`
}
