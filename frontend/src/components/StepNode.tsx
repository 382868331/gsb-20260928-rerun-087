import { Handle, Position, type NodeProps } from "@xyflow/react";
import type { ComponentDef, NodeRunResult } from "../types";

export type StepNodeData = {
  node: { id: string; name: string; type: string };
  component?: ComponentDef;
  result?: NodeRunResult;
  stale: boolean;
  selected: boolean;
};

const statusText: Record<string, string> = {
  succeeded: "成功",
  failed: "失败",
  blocked: "已阻断",
  reused: "复用"
};

export function StepNode({ data }: NodeProps) {
  const step = data as unknown as StepNodeData;
  const result = step.result;
  const className = ["step-node", step.selected ? "selected" : "", step.stale ? "stale" : "", result?.status ?? ""].filter(Boolean).join(" ");
  return (
    <div className={className}>
      {step.component?.inputs.map((port) => <Handle key={port.id} id={port.id} type="target" position={Position.Left} />)}
      <div className="step-category">{step.component?.category ?? "步骤"}</div>
      <strong>{step.node.name}</strong>
      <small>{step.component?.name ?? step.node.type}</small>
      <div className="step-state">{step.stale ? "结果失效" : result ? statusText[result.status] : "未运行"}</div>
      {result?.error && <em>{result.error}</em>}
      {step.component?.outputs.map((port) => <Handle key={port.id} id={port.id} type="source" position={Position.Right} />)}
    </div>
  );
}
