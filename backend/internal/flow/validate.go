package flow

import (
	"fmt"
	"sort"
)

func Validate(project Project) ValidationResult {
	nodes := make(map[string]Node)
	components := make(map[string]Component)
	for _, component := range Components() {
		components[component.Type] = component
	}
	var issues []Issue
	if project.Format != "dataflow.project/v1" {
		issues = append(issues, Issue{Code: "unsupported_format", Message: "工程格式必须是 dataflow.project/v1"})
	}
	if project.ID == "" {
		issues = append(issues, Issue{Code: "missing_project_id", Message: "工程缺少 ID"})
	}
	for _, node := range project.Nodes {
		if node.ID == "" {
			issues = append(issues, Issue{Code: "missing_node_id", Message: "存在没有 ID 的步骤"})
			continue
		}
		if _, exists := nodes[node.ID]; exists {
			issues = append(issues, Issue{Code: "duplicate_node", NodeID: node.ID, Message: "步骤 ID 不能重复"})
		}
		nodes[node.ID] = node
		component, ok := components[node.Type]
		if !ok {
			issues = append(issues, Issue{Code: "unknown_component", NodeID: node.ID, Message: fmt.Sprintf("未知组件：%s", node.Type)})
			continue
		}
		for _, field := range component.Params {
			if field.Required && paramString(node.Params[field.ID]) == "" {
				issues = append(issues, Issue{Code: "missing_parameter", NodeID: node.ID, Message: fmt.Sprintf("%s 缺少参数：%s", node.Name, field.Name)})
			}
		}
	}

	outgoing := make(map[string][]string)
	incoming := make(map[string][]Edge)
	edgeIDs := make(map[string]bool)
	for _, edge := range project.Edges {
		if edge.ID == "" || edgeIDs[edge.ID] {
			issues = append(issues, Issue{Code: "invalid_edge", EdgeID: edge.ID, Message: "连接 ID 为空或重复"})
		}
		edgeIDs[edge.ID] = true
		source, sourceOK := nodes[edge.Source]
		target, targetOK := nodes[edge.Target]
		if !sourceOK || !targetOK {
			issues = append(issues, Issue{Code: "missing_edge_endpoint", EdgeID: edge.ID, Message: "连接引用了不存在的步骤"})
			continue
		}
		sourceComponent := components[source.Type]
		targetComponent := components[target.Type]
		if !portExists(sourceComponent.Outputs, edge.SourcePort) || !portExists(targetComponent.Inputs, edge.TargetPort) {
			issues = append(issues, Issue{Code: "invalid_port", EdgeID: edge.ID, Message: fmt.Sprintf("%s 到 %s 的数据端口不存在", source.Name, target.Name)})
			continue
		}
		outgoing[edge.Source] = append(outgoing[edge.Source], edge.Target)
		incoming[edge.Target] = append(incoming[edge.Target], edge)
	}
	for nodeID, node := range nodes {
		component := components[node.Type]
		for _, input := range component.Inputs {
			connected := 0
			for _, edge := range incoming[nodeID] {
				if edge.TargetPort == input.ID {
					connected++
				}
			}
			if !input.Multi && connected > 1 {
				issues = append(issues, Issue{Code: "multiple_inputs", NodeID: nodeID, Message: fmt.Sprintf("%s 只能连接一个 %s", node.Name, input.Name)})
			}
		}
	}
	for _, node := range project.Nodes {
		component := components[node.Type]
		if component.Type == "file_input" && len(incoming[node.ID]) > 0 {
			issues = append(issues, Issue{Code: "input_not_allowed", NodeID: node.ID, Message: "文件输入不能接收上游数据"})
		}
		if component.Type == "file_output" && len(incoming[node.ID]) == 0 {
			issues = append(issues, Issue{Code: "missing_data_dependency", NodeID: node.ID, Message: "文件输出必须连接数据表"})
		}
		if component.Type == "publish" && len(incoming[node.ID]) == 0 {
			issues = append(issues, Issue{Code: "missing_data_dependency", NodeID: node.ID, Message: "发布到接收方必须连接数据表"})
		}
	}

	order, cycle := topologicalOrder(project.Nodes, outgoing, incoming)
	if len(cycle) > 0 {
		issues = append(issues, Issue{Code: "cycle_detected", Message: fmt.Sprintf("启动前检测到循环连接：%v", cycle)})
	}
	return ValidationResult{OK: len(issues) == 0, Issues: issues, ExecutionOrder: order}
}

func AffectedNodes(project Project, changedNodeIDs []string) []string {
	downstream := make(map[string]bool)
	adjacency := make(map[string][]string)
	for _, edge := range project.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	queue := append([]string(nil), changedNodeIDs...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if downstream[current] {
			continue
		}
		downstream[current] = true
		queue = append(queue, adjacency[current]...)
	}
	var result []string
	for nodeID := range downstream {
		result = append(result, nodeID)
	}
	sort.Strings(result)
	return result
}

func topologicalOrder(nodes []Node, outgoing map[string][]string, incoming map[string][]Edge) ([]string, []string) {
	indegree := make(map[string]int)
	for _, node := range nodes {
		indegree[node.ID] = len(incoming[node.ID])
	}
	queue := make([]string, 0)
	for _, node := range nodes {
		if indegree[node.ID] == 0 {
			queue = append(queue, node.ID)
		}
	}
	sort.Strings(queue)
	order := make([]string, 0, len(nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)
		nextReady := make([]string, 0)
		for _, next := range outgoing[current] {
			indegree[next]--
			if indegree[next] == 0 {
				nextReady = append(nextReady, next)
			}
		}
		sort.Strings(nextReady)
		queue = append(queue, nextReady...)
	}
	if len(order) == len(nodes) {
		return order, nil
	}
	var cycle []string
	for _, node := range nodes {
		if indegree[node.ID] > 0 {
			cycle = append(cycle, node.ID)
		}
	}
	sort.Strings(cycle)
	return order, cycle
}

func portExists(ports []Port, id string) bool {
	for _, port := range ports {
		if port.ID == id {
			return true
		}
	}
	return false
}

func paramString(value any) string {
	if value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Sprint(value)
	}
	return text
}
