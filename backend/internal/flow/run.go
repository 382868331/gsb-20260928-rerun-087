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

func Run(project Project, runID string, startNodeIDs []string, stored map[string]Artifact, outputRoot string) RunResult {
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
		results[nodeID] = NodeResult{NodeID: nodeID, Status: "succeeded", Artifact: &artifact, Reason: reason, Cause: cause}
	}

	affectedList := make([]string, 0, len(affected))
	for nodeID := range affected {
		affectedList = append(affectedList, nodeID)
	}
	sort.Strings(affectedList)
	return RunResult{OK: ok, RunID: runID, ProjectVersion: projectVersion, AffectedNodeIDs: affectedList, Results: results}
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
