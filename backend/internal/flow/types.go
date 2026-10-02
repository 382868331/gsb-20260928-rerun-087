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

// Publication 是“已经对接收方可见”的发布凭证。
// 后续运行失败或撤回未发布修订都不会删除该证据。
type Publication struct {
	NodeID      string `json:"nodeId"`
	Recipient   string `json:"recipient"`
	Version     string `json:"version"`
	RunID       string `json:"runId"`
	Path        string `json:"path,omitempty"`
	Rows        []Row  `json:"rows"`
	Summary     string `json:"summary"`
	PublishedAt string `json:"publishedAt"`
}

type Row map[string]any

type RunRequest struct {
	Project        Project             `json:"project"`
	StartNodeIDs   []string            `json:"startNodeIds"`
	PublishPolicy  string              `json:"publishPolicy"`
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
	NodeID string `json:"nodeId"`
	Status string `json:"status"`
	// Visibility 描述输出步骤对接收方的可见性：
	// published 本轮发布 / already 版本未变不重复发布 / computed 仅已计算 /
	// previous 本轮失败但接收方仍可见旧版本 / none 从未发布。
	Visibility string       `json:"visibility,omitempty"`
	Recipient  string       `json:"recipient,omitempty"`
	Published  *Publication `json:"published,omitempty"`
	Artifact   *Artifact    `json:"artifact,omitempty"`
	Error      string       `json:"error,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	ReusedFrom string       `json:"reusedFrom,omitempty"`
	Cause      string       `json:"cause,omitempty"`
}

const (
	CauseReused      = "reused"
	CauseRerunFull   = "rerun_full"
	CauseRerunParams = "rerun_params"
	CauseRerunInput  = "rerun_input"
	CauseRerunForced = "rerun_forced"
	CauseRerunFirst  = "rerun_first"
)

const (
	PublishAll     = "all"     // 全部结果共同确认：任一接收方分支失败，本轮全部不发布
	PublishPartial = "partial" // 明确允许部分结果先行：成功的输出独立发布
)

const (
	VisibilityPublished = "published" // 本轮已发布，接收方可见
	VisibilityAlready   = "already"   // 已发布过且版本未变，重跑不重复发布
	VisibilityComputed  = "computed"  // 本轮仅完成计算，未对接收方发布
	VisibilityPrevious  = "previous"  // 本轮失败/阻断，接收方仍可见上一已发布版本
	VisibilityNone      = "none"      // 本轮无效且从未发布
)

type RunResult struct {
	OK              bool                   `json:"ok"`
	RunID           string                 `json:"runId"`
	ProjectVersion  string                 `json:"projectVersion"`
	PublishPolicy   string                 `json:"publishPolicy,omitempty"`
	AffectedNodeIDs []string               `json:"affectedNodeIds"`
	Results         map[string]NodeResult  `json:"results"`
	Publications    map[string]Publication `json:"publications,omitempty"`
	Issues          []Issue                `json:"issues,omitempty"`
}

// Normalize 保证空集合序列化为 [] 而不是 null，避免空工程保存后前端崩溃。
func (p *Project) Normalize() {
	if p.Nodes == nil {
		p.Nodes = []Node{}
	}
	if p.Edges == nil {
		p.Edges = []Edge{}
	}
	for index := range p.Nodes {
		if p.Nodes[index].Params == nil {
			p.Nodes[index].Params = map[string]any{}
		}
	}
}
