package flow

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Run(project Project, runID string, startNodeIDs []string, stored map[string]Artifact, deliveries map[string]DeliveryState, outputRoot string) RunResult {
	validation := Validate(project)
	if !validation.OK {
		return RunResult{OK: false, RunID: runID, Issues: validation.Issues}
	}
	affected := make(map[string]bool)
	for _, nodeID := range AffectedNodes(project, startNodeIDs) {
		affected[nodeID] = true
	}
	if len(startNodeIDs) == 0 {
		for _, nodeID := range validation.ExecutionOrder {
			affected[nodeID] = true
		}
	}
	started := make(map[string]bool)
	for _, nodeID := range startNodeIDs {
		started[nodeID] = true
	}
	projectVersion := hashProject(project)
	results := make(map[string]NodeResult)
	artifacts := make(map[string]Artifact)
	for nodeID, artifact := range stored {
		artifacts[nodeID] = artifact
	}
	nodes := make(map[string]Node)
	incoming := make(map[string][]Edge)
	for _, node := range project.Nodes {
		nodes[node.ID] = node
	}
	for _, edge := range project.Edges {
		incoming[edge.Target] = append(incoming[edge.Target], edge)
	}
	// baselineVersions 记录本轮开始前已发布产物的版本，
	// 用于在执行对照中区分“参数变化”与“上游输入变化”。
	baselineVersions := make(map[string]string)
	for nodeID, artifact := range artifacts {
		baselineVersions[nodeID] = artifact.Version
	}
	// 投递状态随本轮运行推进：仅已计算（pending）与已对接收方可见（published）分开记录。
	updatedDeliveries := make(map[string]DeliveryState)
	for nodeID, state := range deliveries {
		updatedDeliveries[nodeID] = state
	}

	ok := true
	for _, nodeID := range validation.ExecutionOrder {
		node := nodes[nodeID]
		if !affected[nodeID] {
			artifact, exists := artifacts[nodeID]
			if !exists {
				ok = false
				results[nodeID] = NodeResult{NodeID: nodeID, Status: "blocked", Error: "缺少上一次运行产物", Reason: "不在本轮重算范围内，也找不到可复用的旧产物", Cause: CauseRerunForced}
				continue
			}
			// 未重算的发布节点：待确认候选若基于旧的流程修订，立即失效，
			// 不能因为分支未被波及就让陈旧候选继续可确认。
			if node.Type == "publish" {
				if state := updatedDeliveries[nodeID]; state.Pending != nil && state.Pending.ProjectVersion != projectVersion {
					updatedDeliveries = invalidatePending(updatedDeliveries, node, runID, "依据的流程修订已变化：来源或步骤在计算后被编辑，旧待确认候选失效")
				}
			}
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "reused", Artifact: &artifact, ReusedFrom: artifact.RunID, Cause: CauseReused, Reason: fmt.Sprintf("参数与上游产物版本未变，沿用 %s 的产物（版本 %s）", artifact.RunID, artifact.Version)}
			continue
		}
		upstreamRows := make([]Row, 0)
		upstreamVersions := make(map[string]string)
		var blockedBy string
		for _, edge := range incoming[nodeID] {
			upstream, exists := results[edge.Source]
			if !exists {
				if artifact, artifactOK := artifacts[edge.Source]; artifactOK {
					upstream = NodeResult{Status: "reused", Artifact: &artifact}
				}
			}
			// 关键判定：只有本轮成功或沿用的产物（允许 0 行空表）才能喂给下游；
			// 上游失败 / 被阻断 / 本轮无结果时，即使磁盘上有旧产物也不能使用。
			if upstream.Status != "succeeded" && upstream.Status != "reused" {
				blockedBy = edge.Source
				break
			}
			if upstream.Artifact == nil {
				blockedBy = edge.Source
				break
			}
			upstreamRows = append(upstreamRows, upstream.Artifact.Rows...)
			upstreamVersions[edge.Source] = upstream.Artifact.Version
		}
		if blockedBy != "" {
			ok = false
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "blocked", Error: fmt.Sprintf("上游 %s 本轮未产出有效数据，拒绝使用旧结果", blockedBy), Reason: "依赖的上游失败或缺失，本轮不会沿用旧产物"}
			delete(artifacts, nodeID)
			if node.Type == "publish" {
				updatedDeliveries = invalidatePending(updatedDeliveries, node, runID, fmt.Sprintf("本轮被阻断：上游 %s 未产出有效数据，待确认候选失去运行结果依据", blockedBy))
			}
			continue
		}
		cause, reason := rerunReason(node, started[nodeID], startNodeIDs, incoming[nodeID], upstreamVersions, baselineVersions, artifacts)
		artifact, err := executeNode(node, upstreamRows, upstreamVersions, runID, outputRoot)
		if err != nil {
			ok = false
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "failed", Error: err.Error(), Reason: "执行失败，已阻断下游并清除旧产物", Cause: cause}
			delete(artifacts, nodeID)
			if node.Type == "publish" {
				updatedDeliveries = invalidatePending(updatedDeliveries, node, runID, "本轮执行失败，待确认候选失去运行结果依据")
			}
			continue
		}
		artifacts[nodeID] = artifact
		if node.Type == "publish" {
			var deliveryNote string
			updatedDeliveries, deliveryNote = recordComputedDelivery(updatedDeliveries, node, artifact, runID, projectVersion)
			reason = reason + "；" + deliveryNote
		}
		results[nodeID] = NodeResult{NodeID: nodeID, Status: "succeeded", Artifact: &artifact, Reason: reason, Cause: cause}
	}

	affectedList := make([]string, 0, len(affected))
	for nodeID := range affected {
		affectedList = append(affectedList, nodeID)
	}
	sort.Strings(affectedList)
	return RunResult{OK: ok, RunID: runID, ProjectVersion: projectVersion, AffectedNodeIDs: affectedList, Results: results, Deliveries: updatedDeliveries}
}

// recordComputedDelivery 把本轮发布节点的计算结果登记为“仅已计算”的待确认投递：
// - 版本与已发布一致时不重复发布，保留原已发布记录；
// - 版本与待确认一致时沿用待确认结果；
// - 共享上游变化导致版本不同时，旧待确认被取代（需重新核对来源），已发布版本保留。
func recordComputedDelivery(deliveries map[string]DeliveryState, node Node, artifact Artifact, runID string, projectVersion string) (map[string]DeliveryState, string) {
	state := deliveries[node.ID]
	state.NodeID = node.ID
	state.Receiver = paramString(node.Params["receiver"])
	if state.History == nil {
		state.History = []DeliveryEvent{}
	}
	computed := Delivery{
		NodeID:         node.ID,
		Receiver:       state.Receiver,
		Version:        artifact.Version,
		RunID:          runID,
		ProjectVersion: projectVersion,
		Rows:           len(artifact.Rows),
		Empty:          len(artifact.Rows) == 0,
		Summary:        artifact.Summary,
	}
	switch {
	case state.Published != nil && state.Published.Version == computed.Version:
		// 已发布版本与本轮一致：不重复发布；若此前有失效候选，本轮成功计算
		// 说明依据已恢复，清除失效标记以免与当前状态混淆。
		state.Invalidated = nil
		deliveries[node.ID] = state
		return deliveries, "版本与已发布结果一致，不重复发布"
	case state.Pending != nil && state.Pending.Version == computed.Version && state.Pending.ProjectVersion == projectVersion:
		state.Invalidated = nil
		deliveries[node.ID] = state
		return deliveries, "待确认版本未变，沿用待确认结果"
	default:
		if state.Pending != nil {
			state.History = append(state.History, DeliveryEvent{Kind: EventSuperseded, Version: state.Pending.Version, RunID: runID, Note: "上游来源变化，旧待确认结果被取代，需重新核对"})
		}
		state.Pending = &computed
		state.Invalidated = nil
		state.History = append(state.History, DeliveryEvent{Kind: EventComputed, Version: computed.Version, RunID: runID, Note: "本轮仅已计算，确认前对接收方不可见"})
		deliveries[node.ID] = state
		return deliveries, "本轮结果仅已计算，确认后才对接收方可见"
	}
}

// invalidatePending 把待确认候选标记为失效：候选本身保留在 Invalidated 供查阅
// 失败前后差异，但不再可被确认；证据日志追加失效原因。
func invalidatePending(deliveries map[string]DeliveryState, node Node, runID string, note string) map[string]DeliveryState {
	state := deliveries[node.ID]
	if state.Pending == nil {
		return deliveries
	}
	if state.NodeID == "" {
		state.NodeID = node.ID
	}
	if state.Receiver == "" {
		state.Receiver = paramString(node.Params["receiver"])
	}
	invalidated := *state.Pending
	state.Invalidated = &invalidated
	state.Pending = nil
	state.History = append(state.History, DeliveryEvent{Kind: EventInvalidated, Version: invalidated.Version, RunID: runID, Note: note})
	deliveries[node.ID] = state
	return deliveries
}

// pendingBasisError 逐项核对待确认候选的依据：来源与流程修订（工程内容哈希）、
// 运行结果（上游产物仍在）以及实际采用的版本关系（上游产物版本重算比对）。
// 返回空串表示候选仍然有效，否则说明失去了哪项依据。
func pendingBasisError(project Project, state DeliveryState, stored map[string]Artifact) string {
	pending := state.Pending
	if pending == nil {
		if state.Invalidated != nil {
			return "上一候选已因依据变化失效（详见投递证据），需重新计算产生新候选后再确认"
		}
		return "没有待确认的发布结果"
	}
	if pending.ProjectVersion != hashProject(project) {
		return "依据的流程修订已变化：来源或步骤在计算后被编辑，该候选已失效"
	}
	var node *Node
	incoming := make([]Edge, 0)
	for index := range project.Nodes {
		if project.Nodes[index].ID == state.NodeID {
			node = &project.Nodes[index]
		}
	}
	if node == nil {
		return "对应的发布步骤已不在当前工程中，该候选已失效"
	}
	for _, edge := range project.Edges {
		if edge.Target == state.NodeID {
			incoming = append(incoming, edge)
		}
	}
	upstreamVersions := make(map[string]string)
	for _, edge := range incoming {
		artifact, exists := stored[edge.Source]
		if !exists {
			return fmt.Sprintf("依据的运行结果已失效：上游 %s 本轮失败或被阻断，没有可用产物", edge.Source)
		}
		upstreamVersions[edge.Source] = artifact.Version
	}
	if expected := nodeVersion(*node, upstreamVersions); expected != pending.Version {
		return "依据的上游产物版本已变化：上游被重算或来源修订，该候选已失效"
	}
	return ""
}

// ConfirmDeliveries 把待确认投递标记为对接收方可见；all 为 true 时要求全部待确认一起确认，
// 否则只允许明确指定的节点部分先行。确认前逐项核对候选依据（来源、流程修订、运行结果），
// 任一目标失效则整体拒绝并把它标记为失效候选；已发布记录与证据日志保留。
func ConfirmDeliveries(deliveries map[string]DeliveryState, nodeIDs []string, all bool, runID string, project Project, stored map[string]Artifact) (map[string]DeliveryState, []string, error) {
	targets := map[string]bool{}
	if all {
		for nodeID, state := range deliveries {
			if state.Pending != nil {
				targets[nodeID] = true
			}
		}
		if len(targets) == 0 {
			return deliveries, nil, fmt.Errorf("没有待确认的发布结果")
		}
	} else {
		for _, nodeID := range nodeIDs {
			targets[nodeID] = true
		}
	}
	confirmed := make([]string, 0, len(targets))
	// 先整体核对：任何一个目标失效都拒绝本次确认，保证全部确认只在
	// 当时每个拟发布结果都满足条件时发生；失效候选就地标记并留证。
	basisErrors := make(map[string]string)
	for nodeID := range targets {
		state, exists := deliveries[nodeID]
		if !exists {
			return deliveries, nil, fmt.Errorf("节点 %s 没有待确认的发布结果", nodeID)
		}
		if basisError := pendingBasisError(project, state, stored); basisError != "" {
			basisErrors[nodeID] = basisError
		}
	}
	if len(basisErrors) > 0 {
		ids := make([]string, 0, len(basisErrors))
		for nodeID := range basisErrors {
			ids = append(ids, nodeID)
		}
		sort.Strings(ids)
		messages := make([]string, 0, len(ids))
		for _, nodeID := range ids {
			if deliveries[nodeID].Pending != nil {
				node := Node{ID: nodeID, Params: map[string]any{"receiver": deliveries[nodeID].Receiver}}
				for index := range project.Nodes {
					if project.Nodes[index].ID == nodeID {
						node = project.Nodes[index]
					}
				}
				deliveries = invalidatePending(deliveries, node, runID, "确认被拒绝："+basisErrors[nodeID])
			}
			messages = append(messages, fmt.Sprintf("节点 %s：%s", nodeID, basisErrors[nodeID]))
		}
		return deliveries, nil, fmt.Errorf("确认被拒绝，候选已失效（%s）", strings.Join(messages, "；"))
	}
	for nodeID := range targets {
		state := deliveries[nodeID]
		published := *state.Pending
		state.Published = &published
		state.Pending = nil
		state.History = append(state.History, DeliveryEvent{Kind: EventPublished, Version: published.Version, RunID: runID, Note: "已确认，结果对接收方可见"})
		deliveries[nodeID] = state
		confirmed = append(confirmed, nodeID)
	}
	sort.Strings(confirmed)
	return deliveries, confirmed, nil
}

// rerunReason 给出执行对照：本轮重算是整流程重算、起点参数变化，
// 还是上游输入（产物版本）变化引发的连带重算。
func rerunReason(node Node, isStart bool, startNodeIDs []string, incomingEdges []Edge, upstreamVersions map[string]string, baselineVersions map[string]string, stored map[string]Artifact) (string, string) {
	if len(startNodeIDs) == 0 {
		return CauseRerunFull, "整流程运行，本轮全部重算"
	}
	inputChanged := false
	changedUpstreams := make([]string, 0)
	for _, edge := range incomingEdges {
		source := edge.Source
		current, currentOK := upstreamVersions[source]
		previous, previousOK := baselineVersions[source]
		if current != previous || currentOK != previousOK {
			inputChanged = true
			changedUpstreams = append(changedUpstreams, source)
		}
	}
	if isStart {
		previous, existed := stored[node.ID]
		paramsChanged := !existed || previous.Version != nodeVersion(node, upstreamVersions)
		switch {
		case !existed:
			return CauseRerunFirst, "局部重跑起点：此前没有已发布产物，首次执行"
		case inputChanged && paramsChanged:
			return CauseRerunParams, "局部重跑起点：参数与上游输入均有变化，重新执行"
		case inputChanged:
			return CauseRerunInput, fmt.Sprintf("局部重跑起点：上游输入 %v 发生变化，重新执行", changedUpstreams)
		case paramsChanged:
			return CauseRerunParams, "局部重跑起点：本步骤参数发生变化，重新执行"
		default:
			return CauseRerunForced, "用户从本步骤手动触发重跑"
		}
	}
	if inputChanged {
		return CauseRerunInput, fmt.Sprintf("上游输入 %v 发生变化，连带重新执行", changedUpstreams)
	}
	return CauseRerunForced, "位于局部重跑范围内，连带重新执行"
}

func executeNode(node Node, upstream []Row, upstreamVersions map[string]string, runID string, outputRoot string) (Artifact, error) {
	version := nodeVersion(node, upstreamVersions)
	var rows []Row
	var summary string
	var outputPath string
	var content string
	switch node.Type {
	case "file_input":
		parsed, err := parseRows(paramString(node.Params["format"]), paramString(node.Params["content"]))
		if err != nil {
			return Artifact{}, fmt.Errorf("文件输入失败：%w", err)
		}
		rows = parsed
		summary = fmt.Sprintf("读取 %s，%d 行", paramString(node.Params["fileName"]), len(rows))
	case "transform":
		var err error
		rows, err = transformRows(upstream, node.Params)
		if err != nil {
			return Artifact{}, err
		}
		summary = fmt.Sprintf("%s 后 %d 行", paramString(node.Params["operation"]), len(rows))
	case "file_output":
		rows = upstream
		encoded, err := encodeRows(paramString(node.Params["format"]), rows)
		if err != nil {
			return Artifact{}, err
		}
		content = encoded
		outputPath, err := safeOutputPath(outputRoot, paramString(node.Params["path"]))
		if err != nil {
			return Artifact{}, err
		}
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return Artifact{}, err
		}
		if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
			return Artifact{}, err
		}
		summary = fmt.Sprintf("写出 %d 行到 %s", len(rows), paramString(node.Params["path"]))
	case "publish":
		rows = upstream
		summary = fmt.Sprintf("向接收方 %s 准备 %d 行", paramString(node.Params["receiver"]), len(rows))
	default:
		return Artifact{}, fmt.Errorf("未知组件：%s", node.Type)
	}
	return Artifact{NodeID: node.ID, Version: version, RunID: runID, Path: outputPath, Content: content, Rows: rows, Summary: summary}, nil
}

func parseRows(format string, content string) ([]Row, error) {
	switch format {
	case "json":
		var rows []Row
		if err := json.Unmarshal([]byte(content), &rows); err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []Row{}
		}
		return rows, nil
	case "csv":
		reader := csv.NewReader(strings.NewReader(content))
		records, err := reader.ReadAll()
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			return nil, errors.New("CSV 没有表头")
		}
		rows := make([]Row, 0, len(records)-1)
		for _, record := range records[1:] {
			row := Row{}
			for index, header := range records[0] {
				if index < len(record) {
					row[header] = record[index]
				}
			}
			rows = append(rows, row)
		}
		return rows, nil
	default:
		return nil, fmt.Errorf("不支持的格式：%s", format)
	}
}

func transformRows(rows []Row, params map[string]any) ([]Row, error) {
	field := paramString(params["field"])
	result := make([]Row, 0, len(rows))
	for _, row := range rows {
		copyRow := Row{}
		for key, value := range row {
			copyRow[key] = value
		}
		switch paramString(params["operation"]) {
		case "passthrough":
		case "filter_equals":
			if fmt.Sprint(copyRow[field]) != paramString(params["value"]) {
				continue
			}
		case "uppercase":
			if field != "" {
				copyRow[field] = strings.ToUpper(fmt.Sprint(copyRow[field]))
			}
		case "lowercase":
			if field != "" {
				copyRow[field] = strings.ToLower(fmt.Sprint(copyRow[field]))
			}
		case "sort":
		default:
			return nil, fmt.Errorf("未知转换操作：%s", paramString(params["operation"]))
		}
		result = append(result, copyRow)
	}
	if paramString(params["operation"]) == "sort" && field != "" {
		sort.SliceStable(result, func(i, j int) bool { return fmt.Sprint(result[i][field]) < fmt.Sprint(result[j][field]) })
	}
	return result, nil
}

func encodeRows(format string, rows []Row) (string, error) {
	switch format {
	case "json":
		encoded, err := json.MarshalIndent(rows, "", "  ")
		return string(encoded), err
	case "csv":
		columns := make([]string, 0)
		seen := make(map[string]bool)
		for _, row := range rows {
			keys := make([]string, 0, len(row))
			for key := range row {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if !seen[key] {
					seen[key] = true
					columns = append(columns, key)
				}
			}
		}
		var builder strings.Builder
		writer := csv.NewWriter(&builder)
		_ = writer.Write(columns)
		for _, row := range rows {
			record := make([]string, 0, len(columns))
			for _, column := range columns {
				record = append(record, fmt.Sprint(row[column]))
			}
			if err := writer.Write(record); err != nil {
				return "", err
			}
		}
		writer.Flush()
		return builder.String(), writer.Error()
	default:
		return "", fmt.Errorf("不支持的输出格式：%s", format)
	}
}

func safeOutputPath(root string, requested string) (string, error) {
	clean := filepath.Clean(requested)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("输出路径必须是相对路径：%s", requested)
	}
	full := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("输出路径不能离开运行目录：%s", requested)
	}
	return full, nil
}

func nodeVersion(node Node, upstreamVersions map[string]string) string {
	payload := struct {
		Node             Node              `json:"node"`
		UpstreamVersions map[string]string `json:"upstreamVersions"`
	}{node, upstreamVersions}
	encoded, _ := json.Marshal(payload)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:12])
}

func hashProject(project Project) string {
	project.Revision = 0
	encoded, _ := json.Marshal(project)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:16])
}
