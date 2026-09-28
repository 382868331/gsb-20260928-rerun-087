import type { ComponentDef, Project, ProjectEdge, ProjectNode } from "./types";

export const emptyProject = (): Project => ({
  format: "dataflow.project/v1",
  id: "first-dataflow",
  name: "第一轮：文件输入到输出",
  revision: 0,
  nodes: [
    { id: "input-1", type: "file_input", name: "读取订单 CSV", position: { x: 50, y: 130 }, params: { fileName: "orders.csv", format: "csv", content: "id,city,amount\n1001,Shanghai,88\n1002,Beijing,42\n1003,Shanghai,120" } },
    { id: "filter-1", type: "transform", name: "只保留上海订单", position: { x: 330, y: 120 }, params: { operation: "filter_equals", field: "city", value: "Shanghai" } },
    { id: "output-1", type: "file_output", name: "写出上海订单", position: { x: 630, y: 130 }, params: { path: "shanghai-orders.json", format: "json" } },
    { id: "input-2", type: "file_input", name: "读取补充订单", position: { x: 50, y: 360 }, params: { fileName: "more-orders.csv", format: "csv", content: "id,city,amount\n2001,Shanghai,55" } },
    { id: "merge-1", type: "transform", name: "分支汇合", position: { x: 330, y: 330 }, params: { operation: "passthrough" } },
    { id: "output-2", type: "file_output", name: "写出汇合结果", position: { x: 630, y: 350 }, params: { path: "merged-orders.csv", format: "csv" } }
  ],
  edges: [
    { id: "e-input-filter", source: "input-1", target: "filter-1", sourcePort: "rows", targetPort: "rows" },
    { id: "e-filter-output", source: "filter-1", target: "output-1", sourcePort: "rows", targetPort: "rows" },
    { id: "e-filter-merge", source: "filter-1", target: "merge-1", sourcePort: "rows", targetPort: "rows" },
    { id: "e-more-merge", source: "input-2", target: "merge-1", sourcePort: "rows", targetPort: "rows" },
    { id: "e-merge-output", source: "merge-1", target: "output-2", sourcePort: "rows", targetPort: "rows" }
  ]
});

export function defaultParams(component: ComponentDef): Record<string, unknown> {
  const params: Record<string, unknown> = {};
  for (const field of component.params) params[field.id] = field.type === "select" ? field.options?.[0] ?? "" : "";
  return params;
}

export function createNode(component: ComponentDef, count: number): ProjectNode {
  return {
    id: component.type + "-" + Date.now() + "-" + count,
    type: component.type,
    name: component.name,
    position: { x: 100 + (count % 4) * 44, y: 100 + count * 36 },
    params: defaultParams(component)
  };
}

export function downstreamIds(project: Project, changedIds: string[]): Set<string> {
  const affected = new Set<string>();
  const queue = [...changedIds];
  while (queue.length) {
    const current = queue.shift()!;
    if (affected.has(current)) continue;
    affected.add(current);
    project.edges.filter((edge) => edge.source === current).forEach((edge) => queue.push(edge.target));
  }
  return affected;
}

export function toReactEdge(edge: ProjectEdge) {
  return { id: edge.id, source: edge.source, target: edge.target, sourceHandle: edge.sourcePort, targetHandle: edge.targetPort };
}
