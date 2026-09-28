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

	ok := true
	for _, nodeID := range validation.ExecutionOrder {
		node := nodes[nodeID]
		if !affected[nodeID] {
			artifact, exists := artifacts[nodeID]
			if !exists {
				ok = false
				results[nodeID] = NodeResult{NodeID: nodeID, Status: "blocked", Error: "缺少上一次运行产物"}
				continue
			}
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "reused", Artifact: &artifact, ReusedFrom: artifact.RunID}
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
			if upstream.Artifact == nil || upstream.Status == "failed" || upstream.Status == "blocked" || upstream.Status == "skipped" {
				blockedBy = edge.Source
				break
			}
			upstreamRows = append(upstreamRows, upstream.Artifact.Rows...)
			upstreamVersions[edge.Source] = upstream.Artifact.Version
		}
		if blockedBy != "" {
			ok = false
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "blocked", Error: fmt.Sprintf("上游 %s 本轮未产出有效数据，拒绝使用旧结果", blockedBy)}
			delete(artifacts, nodeID)
			continue
		}
		artifact, err := executeNode(node, upstreamRows, upstreamVersions, runID, outputRoot)
		if err != nil {
			ok = false
			results[nodeID] = NodeResult{NodeID: nodeID, Status: "failed", Error: err.Error()}
			delete(artifacts, nodeID)
			continue
		}
		artifacts[nodeID] = artifact
		results[nodeID] = NodeResult{NodeID: nodeID, Status: "succeeded", Artifact: &artifact}
	}

	affectedList := make([]string, 0, len(affected))
	for nodeID := range affected {
		affectedList = append(affectedList, nodeID)
	}
	sort.Strings(affectedList)
	return RunResult{OK: ok, RunID: runID, ProjectVersion: projectVersion, AffectedNodeIDs: affectedList, Results: results}
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
