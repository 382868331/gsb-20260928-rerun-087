package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var componentRegistry = map[string]bool{
	"file-input":  true,
	"transform":   true,
	"file-output": true,
	"manual":      true,
}

func ValidateProject(p Project) error {
	if p.ID == "" {
		return errors.New("project id is required")
	}
	if len(p.Nodes) == 0 {
		return errors.New("workflow needs at least one node")
	}
	nodes := map[string]Node{}
	for _, n := range p.Nodes {
		if n.ID == "" {
			return errors.New("node id is required")
		}
		if _, dup := nodes[n.ID]; dup {
			return fmt.Errorf("duplicate node id %s", n.ID)
		}
		if !componentRegistry[n.Type] {
			return fmt.Errorf("unknown component %s on node %s", n.Type, n.ID)
		}
		nodes[n.ID] = n
	}
	seenEdge := map[string]bool{}
	deps := map[string][]string{}
	for _, e := range p.Edges {
		if e.ID == "" {
			return errors.New("edge id is required")
		}
		if seenEdge[e.ID] {
			return fmt.Errorf("duplicate edge id %s", e.ID)
		}
		seenEdge[e.ID] = true
		if _, ok := nodes[e.Source]; !ok {
			return fmt.Errorf("edge %s source missing", e.ID)
		}
		if _, ok := nodes[e.Target]; !ok {
			return fmt.Errorf("edge %s target missing", e.ID)
		}
		if e.Kind != "data" && e.Kind != "parameter" {
			return fmt.Errorf("edge %s must be data or parameter", e.ID)
		}
		if e.Kind == "parameter" && e.Parameter == "" {
			return fmt.Errorf("parameter edge %s requires parameter name", e.ID)
		}
		if e.Source == e.Target {
			return fmt.Errorf("cycle detected at edge %s", e.ID)
		}
		deps[e.Target] = append(deps[e.Target], e.Source)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var walk func(string) error
	walk = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("cycle detected at node %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range deps[id] {
			if err := walk(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range nodes {
		if err := walk(id); err != nil {
			return err
		}
	}
	return nil
}

func topoOrder(p Project) ([]string, error) {
	deps := map[string]map[string]bool{}
	for _, n := range p.Nodes {
		deps[n.ID] = map[string]bool{}
	}
	for _, e := range p.Edges {
		deps[e.Target][e.Source] = true
	}
	ready := []string{}
	for id, d := range deps {
		if len(d) == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	out := []string{}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		for _, e := range p.Edges {
			if e.Source != id {
				continue
			}
			delete(deps[e.Target], id)
			if len(deps[e.Target]) == 0 {
				ready = append(ready, e.Target)
			}
		}
		sort.Strings(ready)
	}
	if len(out) != len(p.Nodes) {
		return nil, errors.New("cycle detected")
	}
	return out, nil
}

func descendants(p Project, start string) map[string]bool {
	out := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, e := range p.Edges {
			if e.Source == id && !out[e.Target] {
				out[e.Target] = true
				queue = append(queue, e.Target)
			}
		}
	}
	return out
}

func nodeByID(p Project, id string) Node {
	for _, n := range p.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}

func preview(lines []string) []string {
	if len(lines) > 8 {
		return append(append([]string{}, lines[:8]...), fmt.Sprintf("... %d more lines", len(lines)-8))
	}
	return append([]string{}, lines...)
}

func executeNode(n Node, incoming [][]string, params map[string]string) ([]string, error) {
	switch n.Type {
	case "file-input":
		content := params["content"]
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("file input %s has empty content", n.Name)
		}
		return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), nil
	case "transform":
		if len(incoming) == 0 {
			return nil, fmt.Errorf("transform %s has no data input", n.Name)
		}
		rows := []string{}
		for _, batch := range incoming {
			rows = append(rows, batch...)
		}
		op := params["operation"]
		suffix := params["suffix"]
		result := make([]string, len(rows))
		for i, row := range rows {
			switch op {
			case "", "uppercase":
				result[i] = strings.ToUpper(row) + suffix
			case "lowercase":
				result[i] = strings.ToLower(row) + suffix
			case "trim":
				result[i] = strings.TrimSpace(row) + suffix
			default:
				return nil, fmt.Errorf("unsupported operation %s", op)
			}
		}
		if op == "reverse-lines" {
			for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
				result[left], result[right] = result[right], result[left]
			}
		}
		return result, nil
	case "file-output":
		if len(incoming) == 0 {
			return nil, fmt.Errorf("output %s has no data input", n.Name)
		}
		rows := []string{}
		for _, batch := range incoming {
			rows = append(rows, batch...)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("output %s received no rows", n.Name)
		}
		return rows, nil
	case "manual":
		if len(incoming) == 0 {
			return nil, fmt.Errorf("manual step %s has no data input", n.Name)
		}
		rows := []string{}
		for _, batch := range incoming {
			rows = append(rows, batch...)
		}
		return rows, nil
	}
	return nil, fmt.Errorf("unknown component %s", n.Type)
}
