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
			continue
		}
		cause, reason := rerunReason(node, started[nodeID], startNodeIDs, incoming[nodeID], upstreamVersions, baselineVersions, artifacts)
		artifact, err := executeNode(node, upstreamRows, upstreamVersions, runID, outputRoot)
		if err != nil {
			ok = false
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "failed", Error: err.Error(), Reason: "执行失败，已阻断下游并清除旧产物", Cause: cause}
			delete(artifacts, nodeID)
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

	// 本轮失败 / 被阻断的发布节点：其遗留待确认候选所依据的运行已经失效，
	// 标记为已失效（保留供查阅失败前后差异），确认时必须拒绝。
	for nodeID, nodeResult := range results {
		if nodeResult.Status != "failed" && nodeResult.Status != "blocked" {
			continue
		}
		node, exists := nodes[nodeID]
		if !exists || node.Type != "publish" {
			continue
		}
		updatedDeliveries = invalidatePendingDelivery(updatedDeliveries, nodeID,
			fmt.Sprintf("本轮运行中该分支%s（%s），待确认结果失去运行依据", statusLabel(nodeResult.Status), nodeResult.Error))
	}

	affectedList := make([]string, 0, len(affected))
	for nodeID := range affected {
		affectedList = append(affectedList, nodeID)
	}
	sort.Strings(affectedList)
	return RunResult{OK: ok, RunID: runID, ProjectVersion: projectVersion, AffectedNodeIDs: affectedList, Results: results, Deliveries: updatedDeliveries}
}

func statusLabel(status string) string {
	if status == "failed" {
		return "执行失败"
	}
	return "被阻断"
}

// invalidatePendingDelivery 把遗留的待确认候选标记为已失效：候选与已发布记录都保留，
// 但确认时会被拒绝并说明失去了哪项依据。
func invalidatePendingDelivery(deliveries map[string]DeliveryState, nodeID string, reason string) map[string]DeliveryState {
	state, exists := deliveries[nodeID]
	if !exists || state.Pending == nil || state.Pending.Stale {
		return deliveries
	}
	pending := *state.Pending
	pending.Stale = true
	pending.StaleReason = reason
	state.Invalidated = appendInvalidated(state.Invalidated, pending)
	state.Pending = &pending
	state.History = append(state.History, DeliveryEvent{Kind: EventInvalidated, Version: pending.Version, RunID: pending.RunID, Note: reason + "，候选已失效，需重新运行产生新候选"})
	deliveries[nodeID] = state
	return deliveries
}

// appendInvalidated 把失效候选追加到留证列表（同版本不重复登记）。
func appendInvalidated(list []Delivery, delivery Delivery) []Delivery {
	for _, existing := range list {
		if existing.NodeID == delivery.NodeID && existing.Version == delivery.Version && existing.RunID == delivery.RunID {
			return list
		}
	}
	return append(list, delivery)
}

// InvalidatePendingForProject 在工程内容（来源或流程修订）发生变化且尚未重跑时，
// 立即把全部遗留待确认候选持久化标记为失效。失效状态不依赖某一次确认请求的现场判断：
// 即使之后把来源改回原文、或不重跑直接确认，旧候选也永远不能复活发布。
func InvalidatePendingForProject(deliveries map[string]DeliveryState, projectVersion string) map[string]DeliveryState {
	if deliveries == nil || projectVersion == "" {
		return deliveries
	}
	updated := deliveries
	for nodeID, state := range deliveries {
		if state.Pending == nil || state.Pending.Stale {
			continue
		}
		if state.Pending.ProjectVersion != "" && state.Pending.ProjectVersion == projectVersion {
			continue
		}
		reason := "候选计算后工程（来源或流程）已被修改，失去流程修订依据"
		updated = invalidatePendingDelivery(updated, nodeID, reason)
	}
	return updated
}

// recordComputedDelivery 把本轮发布节点的计算结果登记为“仅已计算”的待确认投递：
// - 旧候选已失效（依据丢失）时，本轮重算产生全新的待确认版本，旧候选仅留证；
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
	// 已失效的旧候选不能因输出相同而重新生效：先移除（失效证据已在历史里），
	// 本轮结果作为全新的待确认版本登记。
	replacedStale := state.Pending != nil && state.Pending.Stale
	if replacedStale {
		state.Pending = nil
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
		deliveries[node.ID] = state
		if replacedStale {
			return deliveries, "本轮结果与已发布版本一致，不重复发布；失效旧候选已清除留证"
		}
		return deliveries, "版本与已发布结果一致，不重复发布"
	case state.Pending != nil && state.Pending.Version == computed.Version:
		deliveries[node.ID] = state
		return deliveries, "待确认版本未变，沿用待确认结果"
	default:
		if state.Pending != nil {
			state.History = append(state.History, DeliveryEvent{Kind: EventSuperseded, Version: state.Pending.Version, RunID: runID, Note: "上游来源变化，旧待确认结果被取代，需重新核对"})
		}
		state.Pending = &computed
		state.History = append(state.History, DeliveryEvent{Kind: EventComputed, Version: computed.Version, RunID: runID, Note: "本轮仅已计算，确认前对接收方不可见"})
		deliveries[node.ID] = state
		if replacedStale {
			return deliveries, "重新计算成功，产生新的待确认版本（失效旧候选仅留证，确认后才对接收方可见）"
		}
		return deliveries, "本轮结果仅已计算，确认后才对接收方可见"
	}
}

// ConfirmDeliveries 把待确认投递标记为对接收方可见。
//
// 确认请求必须绑定用户实际看过的候选（版本, 运行批次）：targets 携带页面展示的
// Version/RunID，旧页面迟到的确认命中新候选时会被拒绝，不会误发布新结果。
// 共同确认（all）要求覆盖当前所有待确认候选且批次逐一匹配，任一分支失效或批次
// 不一致时整组拒绝（原子语义，绝不部分发布其他分支）；非 all 时允许单个结果先行。
//
// 被拒绝的候选会被持久化标记失效并追加 rejected 证据（即使返回错误，状态变更也随
// 返回的 map 落盘）：来源恢复原文而不重跑不能恢复其发布资格。重复确认已发布的相同
// 版本是幂等的，直接返回成功而不重复发布。
func ConfirmDeliveries(deliveries map[string]DeliveryState, targets []ConfirmTarget, all bool, confirmID string, projectVersion string) (map[string]DeliveryState, []string, error) {
	updated := deliveries
	if updated == nil {
		updated = map[string]DeliveryState{}
	}
	requested := map[string]ConfirmTarget{}
	for _, target := range targets {
		requested[target.NodeID] = target
	}
	if all {
		pendingCount := 0
		for _, state := range updated {
			if state.Pending != nil {
				pendingCount++
			}
		}
		if pendingCount == 0 {
			return updated, nil, fmt.Errorf("没有待确认的发布结果")
		}
		if len(requested) == 0 {
			return updated, nil, fmt.Errorf("共同确认必须绑定当前全部待确认候选的版本与运行批次")
		}
		for nodeID, state := range updated {
			if state.Pending != nil {
				if _, exists := requested[nodeID]; !exists {
					return updated, nil, fmt.Errorf("节点 %s 存在共同确认未覆盖的待确认候选，拒绝部分发布", nodeID)
				}
			}
		}
	}
	nodeIDs := make([]string, 0, len(requested))
	for nodeID := range requested {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	var failures []string
	for _, nodeID := range nodeIDs {
		state, exists := updated[nodeID]
		target := requested[nodeID]
		if !exists {
			failures = append(failures, fmt.Sprintf("节点 %s 没有投递记录", nodeID))
			continue
		}
		if state.Pending == nil {
			if state.Published != nil && (target.Version == "" || state.Published.Version == target.Version) && (target.RunID == "" || state.Published.RunID == target.RunID) {
				continue // 幂等：相同版本已对接收方可见，重复请求不重复发布。
			}
			if state.Published != nil {
				reason := fmt.Sprintf("确认的是旧批次（版本 %s / %s），当前对接收方可见的是版本 %s / %s；旧页面迟到的确认不能发布新结果", displayVersion(target.Version), displayRun(target.RunID), state.Published.Version, state.Published.RunID)
				updated = recordRejection(updated, nodeID, target, reason)
				failures = append(failures, fmt.Sprintf("节点 %s：%s", nodeID, reason))
				continue
			}
			if last := lastInvalidated(state); last != nil {
				reason := "该候选已被拒绝（" + staleReasonOf(last) + "），恢复原内容不能恢复发布资格，需重新运行"
				failures = append(failures, fmt.Sprintf("节点 %s：%s", nodeID, reason))
				continue
			}
			failures = append(failures, fmt.Sprintf("节点 %s 没有待确认的发布结果", nodeID))
			continue
		}
		if reason := rejectReason(state.Pending, projectVersion); reason != "" {
			updated = recordRejection(updated, nodeID, target, reason)
			failures = append(failures, fmt.Sprintf("节点 %s 的待确认结果已失效，拒绝确认：%s", nodeID, reason))
			continue
		}
		if target.Version != "" && target.Version != state.Pending.Version {
			reason := fmt.Sprintf("确认请求绑定的是旧版本 %s，当前待确认版本为 %s（用户看到的候选已被重新计算取代），旧页面迟到的确认拒绝发布", target.Version, state.Pending.Version)
			updated = recordRejection(updated, nodeID, target, reason)
			failures = append(failures, fmt.Sprintf("节点 %s：%s", nodeID, reason))
			continue
		}
		if target.RunID != "" && target.RunID != state.Pending.RunID {
			reason := fmt.Sprintf("确认请求绑定的是旧运行批次 %s，当前候选来自批次 %s，拒绝用旧确认发布新结果", target.RunID, state.Pending.RunID)
			updated = recordRejection(updated, nodeID, target, reason)
			failures = append(failures, fmt.Sprintf("节点 %s：%s", nodeID, reason))
			continue
		}
	}
	if len(failures) > 0 {
		return updated, nil, errors.New(strings.Join(failures, "；")) // 原子语义：任一目标被拒，整组不发布。
	}
	confirmed := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		state := updated[nodeID]
		if state.Pending == nil {
			continue // 幂等命中已发布版本。
		}
		published := *state.Pending
		state.Published = &published
		state.Pending = nil
		state.History = append(state.History, DeliveryEvent{Kind: EventPublished, Version: published.Version, RunID: confirmID, Note: fmt.Sprintf("已确认（候选来自 %s，版本 %s），结果对接收方可见", published.RunID, published.Version)})
		updated[nodeID] = state
		confirmed = append(confirmed, nodeID)
	}
	return updated, confirmed, nil
}

func displayVersion(version string) string {
	if version == "" {
		return "未知版本"
	}
	return version
}

func displayRun(runID string) string {
	if runID == "" {
		return "未知批次"
	}
	return runID
}

// recordRejection 把一次拒绝确认持久化：待确认候选标记失效并转入留证列表，
// 历史追加 rejected 事件。之后即使来源恢复原文且不重跑，也不能再确认它。
func recordRejection(deliveries map[string]DeliveryState, nodeID string, target ConfirmTarget, reason string) map[string]DeliveryState {
	state := deliveries[nodeID]
	targetVersion := target.Version
	targetRunID := target.RunID
	if state.Pending != nil {
		matchesBatch := (target.Version == "" || target.Version == state.Pending.Version) && (target.RunID == "" || target.RunID == state.Pending.RunID)
		if matchesBatch && !state.Pending.Stale {
			pending := *state.Pending
			pending.Stale = true
			pending.StaleReason = reason
			state.Invalidated = appendInvalidated(state.Invalidated, pending)
			state.Pending = &pending
		} else if !matchesBatch {
			// 旧页面迟到的确认针对的是已被取代的旧候选：当前新候选保持有效，
			// 只登记拒绝证据，不能误伤新版本。
			targetVersion = target.Version
			targetRunID = target.RunID
		} else {
			targetVersion = state.Pending.Version
			targetRunID = state.Pending.RunID
		}
	}
	state.History = append(state.History, DeliveryEvent{Kind: EventRejected, Version: targetVersion, RunID: targetRunID, Note: "确认被拒绝：" + reason})
	deliveries[nodeID] = state
	return deliveries
}

func lastInvalidated(state DeliveryState) *Delivery {
	if len(state.Invalidated) == 0 {
		return nil
	}
	return &state.Invalidated[len(state.Invalidated)-1]
}

func staleReasonOf(delivery *Delivery) string {
	if delivery.StaleReason != "" {
		return delivery.StaleReason
	}
	return "来源运行失败或流程修订变化，候选失去依据"
}

// rejectReason 返回候选不可确认的原因（失去了哪项依据）；可确认时返回空串。
func rejectReason(pending *Delivery, projectVersion string) string {
	if pending.Stale {
		return staleReasonOf(pending)
	}
	if projectVersion != "" && pending.ProjectVersion != "" && pending.ProjectVersion != projectVersion {
		return "候选计算后工程（来源或流程）已被修改，失去流程修订依据，需重新运行产生新候选"
	}
	return ""
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

// ProjectVersion 返回工程内容（不计 revision 号）的版本哈希，
// 用于确认时核对待确认候选所依据的流程修订。
func ProjectVersion(project Project) string {
	return hashProject(project)
}
