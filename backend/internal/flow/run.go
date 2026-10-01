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
	previous := make(map[string]Artifact)
	for nodeID, artifact := range stored {
		artifacts[nodeID] = artifact
		previous[nodeID] = artifact
	}
	nodes := make(map[string]Node)
	incoming := make(map[string][]Edge)
	for _, node := range project.Nodes {
		nodes[node.ID] = node
	}
	for _, edge := range project.Edges {
		incoming[edge.Target] = append(incoming[edge.Target], edge)
	}

	ok := true
	for _, nodeID := range validation.ExecutionOrder {
		node := nodes[nodeID]
		if !affected[nodeID] {
			artifact, exists := artifacts[nodeID]
			if !exists {
				ok = false
				results[nodeID] = NodeResult{NodeID: nodeID, Status: "blocked", Error: "缺少上一次运行产物", Reason: "不在本轮重算范围内，也找不到可复用的旧产物"}
				continue
			}
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "reused", Artifact: &artifact, ReusedFrom: artifact.RunID, ChangeBasis: "unchanged", PrevVersion: artifact.Version, Reason: fmt.Sprintf("参数与上游输入版本均未变，沿用 %s 的原结果（版本 %s）", artifact.RunID, artifact.Version)}
			continue
		}
		upstreamRows := make([]Row, 0)
		upstreamVersions := make(map[string]string)
		var blockedBy string
		var blockedReason string
		for _, edge := range incoming[nodeID] {
			upstream, exists := results[edge.Source]
			if !exists {
				if artifact, artifactOK := artifacts[edge.Source]; artifactOK {
					upstream = NodeResult{Status: "reused", Artifact: &artifact}
				}
			}
			if upstream.Artifact == nil {
				blockedBy = edge.Source
				blockedReason = fmt.Sprintf("共享上游 %s 本轮没有有效产物（可能空输出或尚未执行）", edge.Source)
				break
			}
			if upstream.Status == "failed" || upstream.Status == "blocked" || upstream.Status == "skipped" {
				blockedBy = edge.Source
				blockedReason = fmt.Sprintf("共享上游 %s 本轮%s", edge.Source, statusDescription(upstream.Status))
				break
			}
			upstreamRows = append(upstreamRows, upstream.Artifact.Rows...)
			upstreamVersions[edge.Source] = upstream.Artifact.Version
		}
		if blockedBy != "" {
			ok = false
			errorText := blockedReason
			if errorText == "" {
				errorText = fmt.Sprintf("上游 %s 本轮未产出有效数据", blockedBy)
			}
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "blocked", Error: errorText + "，拒绝使用旧成功结果冒充本轮数据", Reason: "依赖的共享上游失败或空产出，本轮不会沿用旧产物"}
			delete(artifacts, nodeID)
			continue
		}
		paramsVer := paramsVersion(node)
		reason := "整流程运行，本轮全部重算"
		basis := "full"
		if len(startNodeIDs) > 0 {
			if started[nodeID] {
				basis = classifyChange(node, paramsVer, upstreamVersions, previous)
				if basis == "downstream" || basis == "inputs" {
					basis = "start"
				}
				switch basis {
				case "params":
					reason = "局部重跑起点：按用户修订重新执行（本步骤参数已变化）"
				case "params_and_inputs":
					reason = "局部重跑起点：按用户修订重新执行（参数与上游输入均已变化）"
				default:
					reason = "局部重跑起点：本步骤被指定为重跑入口，重新执行"
				}
			} else {
				basis = classifyChange(node, paramsVer, upstreamVersions, previous)
				switch basis {
				case "params":
					reason = "上游输入版本未变，因本步骤参数发生变化而重新执行"
				case "inputs":
					reason = describeInputChange(nodeID, incoming[nodeID], previous) + "，本步骤参数未变，重新执行"
				case "params_and_inputs":
					reason = describeInputChange(nodeID, incoming[nodeID], previous) + "，且本步骤参数也发生变化，重新执行"
				default:
					reason = "处于局部重跑的数据下游，连带重新执行"
				}
			}
		}
		artifact, err := executeNode(node, upstreamRows, upstreamVersions, paramsVer, runID, outputRoot)
		if err != nil {
			ok = false
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "failed", Error: err.Error(), Reason: "执行失败，已阻断下游并清除旧产物"}
			delete(artifacts, nodeID)
			continue
		}
		artifacts[nodeID] = artifact
		resultEntry := NodeResult{NodeID: nodeID, Status: "succeeded", Artifact: &artifact, Reason: reason, ChangeBasis: basis}
		if old, exists := previous[nodeID]; exists {
			resultEntry.PrevVersion = old.Version
		}
		results[nodeID] = resultEntry
	}

	affectedList := make([]string, 0, len(affected))
	for nodeID := range affected {
		affectedList = append(affectedList, nodeID)
	}
	sort.Strings(affectedList)
	return RunResult{OK: ok, RunID: runID, ProjectVersion: projectVersion, AffectedNodeIDs: affectedList, Results: results}
}

func executeNode(node Node, upstream []Row, upstreamVersions map[string]string, paramsVer string, runID string, outputRoot string) (Artifact, error) {
	version := nodeVersion(paramsVer, upstreamVersions)
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
	if rows == nil {
		rows = []Row{}
	}
	return Artifact{NodeID: node.ID, Version: version, ParamsVersion: paramsVer, InputVersions: copyVersions(upstreamVersions), RunID: runID, Path: outputPath, Content: content, Rows: rows, Summary: summary}, nil
}

func parseRows(format string, content string) ([]Row, error) {
	switch format {
	case "json":
		var rows []Row
		if err := json.Unmarshal([]byte(content), &rows); err != nil {
			return nil, err
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

func paramsVersion(node Node) string {
	encoded, _ := json.Marshal(struct {
		Type   string         `json:"type"`
		Params map[string]any `json:"params"`
	}{node.Type, node.Params})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:12])
}

func nodeVersion(paramsVer string, upstreamVersions map[string]string) string {
	encoded, _ := json.Marshal(struct {
		ParamsVersion    string            `json:"paramsVersion"`
		UpstreamVersions map[string]string `json:"upstreamVersions"`
	}{paramsVer, sortedCopy(upstreamVersions)})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:12])
}

func sortedCopy(input map[string]string) map[string]string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(map[string]string, len(input))
	for _, key := range keys {
		result[key] = input[key]
	}
	return result
}

func copyVersions(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	return sortedCopy(input)
}

// classifyChange compares the current parameter hash and upstream input
// hashes against the artifact of the last saved run, so the revision diff can
// state whether a node was rerun for its own params, for changed inputs, or
// for both.
func classifyChange(node Node, paramsVer string, upstreamVersions map[string]string, previous map[string]Artifact) string {
	old, exists := previous[node.ID]
	paramsChanged := !exists || old.ParamsVersion != paramsVer
	inputsChanged := false
	if !exists {
		inputsChanged = len(upstreamVersions) > 0
	} else if len(old.InputVersions) == 0 && old.ParamsVersion == "" {
		// Artifact produced before the versioned comparison existed: infer its
		// input set from the versions of the artifacts still kept on disk.
		inputsChanged = len(upstreamVersions) > 0 && !versionsMatchStored(upstreamVersions, previous)
	} else {
		inputsChanged = !sameVersions(old.InputVersions, upstreamVersions)
	}
	switch {
	case paramsChanged && inputsChanged:
		return "params_and_inputs"
	case paramsChanged:
		return "params"
	case inputsChanged:
		return "inputs"
	default:
		return "downstream"
	}
}

func versionsMatchStored(upstreamVersions map[string]string, previous map[string]Artifact) bool {
	for source, version := range upstreamVersions {
		stored, ok := previous[source]
		if !ok || stored.Version != version {
			return false
		}
	}
	return true
}

func sameVersions(old map[string]string, current map[string]string) bool {
	if len(old) != len(current) {
		return false
	}
	for key, value := range current {
		if old[key] != value {
			return false
		}
	}
	return true
}

func describeInputChange(nodeID string, edges []Edge, previous map[string]Artifact) string {
	old, exists := previous[nodeID]
	changed := make([]string, 0)
	for _, edge := range edges {
		if !exists || old.InputVersions[edge.Source] == "" {
			changed = append(changed, "新增来自 "+edge.Source+" 的数据输入")
		}
	}
	if len(changed) > 0 {
		return strings.Join(changed, "；")
	}
	return "上游数据输入版本发生变化"
}

func statusDescription(status string) string {
	switch status {
	case "failed":
		return "执行失败"
	case "blocked":
		return "已被阻断"
	case "skipped":
		return "被跳过"
	default:
		return "未产出有效数据"
	}
}

func hashProject(project Project) string {
	project.Revision = 0
	encoded, _ := json.Marshal(project)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:16])
}
