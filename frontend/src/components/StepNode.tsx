import { Handle, Position, type NodeProps } from "@xyflow/react";
import type { ComponentDef, NodeRunResult } from "../types";

export type StepNodeData = {
  node: { id: string; name: string; type: string };
  component?: ComponentDef;
  result?: NodeRunResult;
  stale: boolean;
  selected: boolean;
};

export const statusText: Record<string, string> = {
  succeeded: "成功",
  failed: "失败",
  blocked: "已阻断",
  reused: "复用"
};

const basisText: Record<string, string> = {
  full: "整流程重算",
  start: "重跑起点",
  params: "参数变化重算",
  inputs: "输入变化重算",
  params_and_inputs: "参数与输入均变化",
  downstream: "连带重算",
  unchanged: "沿用原结果"
};

export function describeBasis(basis?: string): string {
  return basis ? basisText[basis] ?? basis : "";
}

export function StepNode({ data }: NodeProps) {
  const step = data as unknown as StepNodeData;
  const result = step.result;
  const className = ["step-node", step.selected ? "selected" : "", step.stale ? "stale" : "", result?.status ?? ""].filter(Boolean).join(" ");
  const inputs = step.component?.inputs ?? [];
  const outputs = step.component?.outputs ?? [];
  return (
    <div className={className}>
      {inputs.map((port) => <Handle key={port.id} id={port.id} type="target" position={Position.Left} />)}
      <div className="step-category">{step.component?.category ?? "步骤"}</div>
      <strong>{step.node.name}</strong>
      <small>{step.component?.name ?? step.node.type}</small>
      <div className="step-state">{step.stale ? "结果失效" : result ? statusText[result.status] : "未运行"}</div>
      {result?.error && <em>{result.error}</em>}
      {outputs.map((port) => <Handle key={port.id} id={port.id} type="source" position={Position.Right} />)}
    </div>
  );
}
