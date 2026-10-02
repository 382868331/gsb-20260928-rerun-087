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

export const visibilityText: Record<string, string> = {
  published: "已发布·接收方可见",
  already: "版本未变·未重复发布",
  computed: "仅已计算·未发布",
  previous: "接收方仍见旧版",
  none: "从未发布"
};

export function StepNode({ data }: NodeProps) {
  const step = data as unknown as StepNodeData;
  const result = step.result;
  const inputs = step.component?.inputs ?? [];
  const outputs = step.component?.outputs ?? [];
  const className = ["step-node", step.selected ? "selected" : "", step.stale ? "stale" : "", result?.status ?? ""].filter(Boolean).join(" ");
  return (
    <div className={className}>
      {inputs.map((port) => <Handle key={port.id} id={port.id} type="target" position={Position.Left} />)}
      <div className="step-category">{step.component?.category ?? "步骤"}</div>
      <strong>{step.node.name}</strong>
      <small>{step.component?.name ?? step.node.type}</small>
      <div className="step-state">{step.stale ? "结果失效" : result ? statusText[result.status] : "未运行"}</div>
      {result?.visibility && <div className={"step-visibility " + result.visibility}>{visibilityText[result.visibility]}{result.recipient ? `（${result.recipient}）` : ""}</div>}
      {result?.error && <em>{result.error}</em>}
      {outputs.map((port) => <Handle key={port.id} id={port.id} type="source" position={Position.Right} />)}
    </div>
  );
}
