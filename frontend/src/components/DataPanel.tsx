import type { Project, RunResult } from "../types";
import { statusText, visibilityText } from "./StepNode";

type Props = { project: Project; selectedId?: string; run?: RunResult; onSelectNode?: (id: string) => void };

const causeText: Record<string, string> = {
  reused: "沿用原结果",
  rerun_full: "整流程重算",
  rerun_params: "参数变化，重新执行",
  rerun_input: "输入变化，重新执行",
  rerun_forced: "手动触发重跑",
  rerun_first: "首次执行"
};

export function DataPanel({ project, selectedId, run, onSelectNode }: Props) {
  const selected = project.nodes.find((node) => node.id === selectedId);
  const result = selectedId ? run?.results[selectedId] : undefined;
  const rows = result?.artifact?.rows ?? [];
  return <section className="data-panel">
    <div className="panel-title"><h2>实际数据与局部重跑范围</h2>{run && <span>运行批次：{run.runId}</span>}</div>
    {!selected ? <p className="muted">选择步骤查看本轮真实数据；失败或被阻断的步骤不会显示旧数据。</p> : (
      <>
        <h3>{selected.name}</h3>
        {!result?.artifact ? <div className="banner danger">{result?.error ?? "当前没有本轮有效产物"}</div> : (
          <>
            <p>{result.artifact.summary} · 版本 {result.artifact.version}{result.reusedFrom ? ` · 复用 ${result.reusedFrom}` : ""}</p>
            {result.artifact.path && <p>输出文件：{result.artifact.path}</p>}
            {rows.length === 0 ? <div className="banner">本轮执行成功，输出为空（0 行），不是旧数据。</div> : <div className="table-wrap">
              <table>
                <thead><tr>{Object.keys(rows[0] ?? {}).map((key) => <th key={key}>{key}</th>)}</tr></thead>
                <tbody>{rows.slice(0, 8).map((row, index) => <tr key={index}>{Object.entries(row).map(([key, value]) => <td key={key}>{String(value)}</td>)}</tr>)}</tbody>
              </table>
            </div>}
          </>
        )}
        {selected.type === "file_output" && <div className="recipient-card">
          <b>接收方：{String(selected.params.recipient ?? selected.name)}</b>
          {result?.visibility ? <span className={"visibility-pill " + result.visibility}>{visibilityText[result.visibility] ?? result.visibility}</span> : <span className="visibility-pill none">尚未运行发布</span>}
          {result?.published && <em>已发布证据：{result.published.runId} · 版本 {result.published.version} · {result.published.publishedAt}{result.published.path ? ` · 文件 ${result.published.path}` : ""}</em>}
        </div>}
      </>
    )}
    {run && <div className="scope"><b>本轮范围：</b>{run.affectedNodeIds.map((id) => project.nodes.find((node) => node.id === id)?.name ?? id).join(" → ")}</div>}
    {run && <div className="reuse-report"><b>修订后的执行对照与接收方可见性{publishPolicyText(run.publishPolicy)}</b>
      {project.nodes.map((node) => {
        const stepResult = run.results[node.id];
        if (!stepResult) return null;
        return <div key={node.id} data-node-id={node.id} className={"reuse-row clickable " + stepResult.status} onClick={() => selectedId !== node.id && onSelectNode?.(node.id)}><span>{node.name}{node.type === "file_output" ? ` → ${String(node.params.recipient ?? node.name)}` : ""}</span><b>{statusText[stepResult.status] ?? stepResult.status}{stepResult.cause ? ` · ${causeText[stepResult.cause] ?? stepResult.cause}` : ""}{stepResult.visibility ? ` · ${visibilityText[stepResult.visibility] ?? stepResult.visibility}` : ""}</b><em>{stepResult.reason ?? stepResult.error ?? ""}</em></div>;
      })}
    </div>}
  </section>;
}

function publishPolicyText(policy?: string) {
  if (policy === "all") return "（全部共同确认：有分支失败则一个都不发布）";
  if (policy === "partial") return "（允许部分结果先行）";
  return "";
}
