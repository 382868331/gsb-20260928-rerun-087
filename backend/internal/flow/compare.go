package flow

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Candidate 是跨运行归档的发布候选快照：每次发布节点成功计算都会留下一条
// 独立记录（键 = 节点 + 输出版本 + 运行批次）。输出完全相同但来自不同运行 /
// 不同流程修订的候选是两条不同记录，绝不合并。
type Candidate struct {
	Key            string `json:"key"`
	NodeID         string `json:"nodeId"`
	Receiver       string `json:"receiver"`
	Version        string `json:"version"`
	RunID          string `json:"runId"`
	ProjectVersion string `json:"projectVersion"`
	Rows           []Row  `json:"rows"`
	Summary        string `json:"summary"`
}

// CandidateStatus 候选的发布结果状态（由投递状态与证据日志推导，不落库）。
const (
	StatusPending    = "pending"
	StatusPublished  = "published"
	StatusStale      = "stale"
	StatusSuperseded = "superseded"
)

// CandidateFilter 候选筛选条件：接收方、来源修订（流程版本）、运行时间区间、发布结果。
type CandidateFilter struct {
	Receiver       string `json:"receiver,omitempty"`
	ProjectVersion string `json:"projectVersion,omitempty"`
	RunFrom        string `json:"runFrom,omitempty"`
	RunTo          string `json:"runTo,omitempty"`
	Status         string `json:"status,omitempty"`
}

// CandidateView 是候选的列表视图（不含输出行，行内容在对比接口里返回）。
type CandidateView struct {
	Key            string `json:"key"`
	NodeID         string `json:"nodeId"`
	Receiver       string `json:"receiver"`
	Version        string `json:"version"`
	RunID          string `json:"runId"`
	ProjectVersion string `json:"projectVersion"`
	Status         string `json:"status"`
	Rows           int    `json:"rows"`
	Empty          bool   `json:"empty"`
	Summary        string `json:"summary"`
}

// CompareView 是保存的筛选条件（含“只看差异”开关），随工程持久化，
// 重新打开工程后可以继续核对。
type CompareView struct {
	Name     string          `json:"name"`
	Filter   CandidateFilter `json:"filter"`
	DiffOnly bool            `json:"diffOnly"`
}

// FieldChange 描述同一行在两个候选之间的字段变化。
type FieldChange struct {
	Field string `json:"field"`
	A     any    `json:"a"`
	B     any    `json:"b"`
}

// RowChange 描述按行键配对后的一行字段变化。
type RowChange struct {
	RowKey string        `json:"rowKey"`
	Fields []FieldChange `json:"fields"`
}

// BasisDiff 描述两个候选的计算依据差异（接收方 / 来源修订 / 运行批次 / 发布结果）。
type BasisDiff struct {
	Field string `json:"field"`
	A     string `json:"a"`
	B     string `json:"b"`
}

// Comparison 是两个候选的并排对比结果。
type Comparison struct {
	A          CandidateView `json:"a"`
	B          CandidateView `json:"b"`
	Added      []Row         `json:"added"`
	Removed    []Row         `json:"removed"`
	Changes    []RowChange   `json:"changes"`
	Identical  bool          `json:"identical"`
	BasisDiffs []BasisDiff   `json:"basisDiffs"`
}

// CandidateKey 生成候选键：节点 + 输出版本 + 运行批次。
// 同一输出在不同运行里重算会得到不同键，保证依据不同的候选不被合并。
func CandidateKey(nodeID string, version string, runID string) string {
	return nodeID + "|" + version + "|" + runID
}

// RecordCandidate 把本轮发布节点的计算结果归档为候选；同一键重复登记幂等。
func RecordCandidate(candidates map[string]Candidate, node Node, artifact Artifact, runID string, projectVersion string) map[string]Candidate {
	if candidates == nil {
		candidates = map[string]Candidate{}
	}
	key := CandidateKey(node.ID, artifact.Version, runID)
	if _, exists := candidates[key]; exists {
		return candidates
	}
	rows := artifact.Rows
	if rows == nil {
		rows = []Row{}
	}
	candidates[key] = Candidate{
		Key:            key,
		NodeID:         node.ID,
		Receiver:       paramString(node.Params["receiver"]),
		Version:        artifact.Version,
		RunID:          runID,
		ProjectVersion: projectVersion,
		Rows:           rows,
		Summary:        artifact.Summary,
	}
	return candidates
}

// CandidateStatus 从投递状态与证据日志推导候选当前的发布结果状态：
// 待确认 / 已发布 / 已失效 / 被取代。推导是只读的，不改变任何投递状态。
func CandidateStatus(state DeliveryState, candidate Candidate) string {
	if state.Pending != nil && state.Pending.Version == candidate.Version && state.Pending.RunID == candidate.RunID {
		if state.Pending.Stale {
			return StatusStale
		}
		return StatusPending
	}
	if state.Published != nil && state.Published.Version == candidate.Version && state.Published.RunID == candidate.RunID {
		return StatusPublished
	}
	status := StatusSuperseded
	for _, event := range state.History {
		if event.Version != candidate.Version {
			continue
		}
		switch event.Kind {
		case EventPublished:
			status = StatusPublished
		case EventInvalidated, EventRejected:
			status = StatusStale
		case EventSuperseded:
			status = StatusSuperseded
		}
	}
	return status
}

// ListCandidates 按筛选条件返回候选列表视图（按运行时间倒序，新的在前）。
// 运行批次格式为 run-20060102T150405…，字典序与时间序一致，可直接比较区间。
func ListCandidates(candidates map[string]Candidate, deliveries map[string]DeliveryState, filter CandidateFilter) []CandidateView {
	views := make([]CandidateView, 0, len(candidates))
	for _, candidate := range candidates {
		status := CandidateStatus(deliveries[candidate.NodeID], candidate)
		view := CandidateView{
			Key:            candidate.Key,
			NodeID:         candidate.NodeID,
			Receiver:       candidate.Receiver,
			Version:        candidate.Version,
			RunID:          candidate.RunID,
			ProjectVersion: candidate.ProjectVersion,
			Status:         status,
			Rows:           len(candidate.Rows),
			Empty:          len(candidate.Rows) == 0,
			Summary:        candidate.Summary,
		}
		if !matchFilter(view, filter) {
			continue
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].RunID != views[j].RunID {
			return views[i].RunID > views[j].RunID
		}
		return views[i].Key < views[j].Key
	})
	return views
}

func matchFilter(view CandidateView, filter CandidateFilter) bool {
	if filter.Receiver != "" && view.Receiver != filter.Receiver {
		return false
	}
	if filter.ProjectVersion != "" && view.ProjectVersion != filter.ProjectVersion {
		return false
	}
	if filter.RunFrom != "" && view.RunID < filter.RunFrom {
		return false
	}
	if filter.RunTo != "" && view.RunID > filter.RunTo {
		return false
	}
	if filter.Status != "" && view.Status != filter.Status {
		return false
	}
	return true
}

// CompareCandidates 并排对比两个候选：输入修订（来源修订 / 运行批次等计算依据）
// 与输出记录的新增、删除和字段变化。输出完全相同但依据不同的候选会明确列出
// 依据差异（Identical=true 且 BasisDiffs 非空），两条记录保持独立不合并。
// 本函数是纯函数：不修改投递状态，不影响接收方可见版本，也不改变失效候选的资格。
func CompareCandidates(a Candidate, b Candidate, deliveries map[string]DeliveryState) Comparison {
	viewA := CandidateView{Key: a.Key, NodeID: a.NodeID, Receiver: a.Receiver, Version: a.Version, RunID: a.RunID, ProjectVersion: a.ProjectVersion, Status: CandidateStatus(deliveries[a.NodeID], a), Rows: len(a.Rows), Empty: len(a.Rows) == 0, Summary: a.Summary}
	viewB := CandidateView{Key: b.Key, NodeID: b.NodeID, Receiver: b.Receiver, Version: b.Version, RunID: b.RunID, ProjectVersion: b.ProjectVersion, Status: CandidateStatus(deliveries[b.NodeID], b), Rows: len(b.Rows), Empty: len(b.Rows) == 0, Summary: b.Summary}
	comparison := Comparison{A: viewA, B: viewB, Added: []Row{}, Removed: []Row{}, Changes: []RowChange{}, BasisDiffs: []BasisDiff{}}

	keyField := rowKeyField(a.Rows, b.Rows)
	rowsA := indexRows(a.Rows, keyField)
	rowsB := indexRows(b.Rows, keyField)
	keys := make([]string, 0, len(rowsA)+len(rowsB))
	seen := map[string]bool{}
	for _, key := range orderedKeys(a.Rows, keyField) {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for _, key := range orderedKeys(b.Rows, keyField) {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		rowA, inA := rowsA[key]
		rowB, inB := rowsB[key]
		switch {
		case inA && !inB:
			comparison.Removed = append(comparison.Removed, rowA)
		case !inA && inB:
			comparison.Added = append(comparison.Added, rowB)
		default:
			if change := diffRow(key, rowA, rowB); len(change.Fields) > 0 {
				comparison.Changes = append(comparison.Changes, change)
			}
		}
	}
	comparison.Identical = len(comparison.Added) == 0 && len(comparison.Removed) == 0 && len(comparison.Changes) == 0

	addBasis := func(field string, va string, vb string) {
		if va != vb {
			comparison.BasisDiffs = append(comparison.BasisDiffs, BasisDiff{Field: field, A: va, B: vb})
		}
	}
	addBasis("接收方", viewA.Receiver, viewB.Receiver)
	addBasis("来源修订", viewA.ProjectVersion, viewB.ProjectVersion)
	addBasis("运行批次", viewA.RunID, viewB.RunID)
	addBasis("发布结果", viewA.Status, viewB.Status)
	return comparison
}

// rowKeyField 选择行配对键：两侧所有行都有 id 字段时按 id 配对，否则按行号配对。
func rowKeyField(a []Row, b []Row) string {
	hasID := func(rows []Row) bool {
		if len(rows) == 0 {
			return false
		}
		for _, row := range rows {
			if _, ok := row["id"]; !ok {
				return false
			}
		}
		return true
	}
	if hasID(a) && hasID(b) {
		return "id"
	}
	return ""
}

func rowKey(row Row, keyField string, index int) string {
	if keyField == "" {
		return fmt.Sprintf("#%d", index)
	}
	return fmt.Sprintf("%s=%v", keyField, row[keyField])
}

func orderedKeys(rows []Row, keyField string) []string {
	keys := make([]string, 0, len(rows))
	for index, row := range rows {
		keys = append(keys, rowKey(row, keyField, index))
	}
	return keys
}

func indexRows(rows []Row, keyField string) map[string]Row {
	indexed := make(map[string]Row, len(rows))
	for index, row := range rows {
		indexed[rowKey(row, keyField, index)] = row
	}
	return indexed
}

func diffRow(key string, a Row, b Row) RowChange {
	change := RowChange{RowKey: key, Fields: []FieldChange{}}
	fields := map[string]bool{}
	for field := range a {
		fields[field] = true
	}
	for field := range b {
		fields[field] = true
	}
	sorted := make([]string, 0, len(fields))
	for field := range fields {
		sorted = append(sorted, field)
	}
	sort.Strings(sorted)
	for _, field := range sorted {
		va, inA := a[field]
		vb, inB := b[field]
		if inA != inB || canonicalJSON(va) != canonicalJSON(vb) {
			change.Fields = append(change.Fields, FieldChange{Field: field, A: va, B: vb})
		}
	}
	return change
}

func canonicalJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// SaveCompareView 保存（按名称替换）一个筛选视图；视图只是查询条件，
// 保存与删除都不会改变候选或投递状态。
func SaveCompareView(views []CompareView, view CompareView) ([]CompareView, error) {
	if view.Name == "" {
		return views, fmt.Errorf("视图名称不能为空")
	}
	replaced := false
	for index, existing := range views {
		if existing.Name == view.Name {
			views[index] = view
			replaced = true
			break
		}
	}
	if !replaced {
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

// DeleteCompareView 按名称删除筛选视图。
func DeleteCompareView(views []CompareView, name string) []CompareView {
	kept := make([]CompareView, 0, len(views))
	for _, view := range views {
		if view.Name != name {
			kept = append(kept, view)
		}
	}
	return kept
}
