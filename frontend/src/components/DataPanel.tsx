import type { Project, RunResult } from "../types";
import { statusText } from "./StepNode";

type Props = { project: Project; selectedId?: string; run?: RunResult };

export function DataPanel({ project, selectedId, run }: Props) {
  const selected = project.nodes.find((node) => node.id === selectedId);
  const result = selectedId ? run?.results[selectedId] : undefined;
  return <section className="data-panel">
    <div className="panel-title"><h2>实际数据与局部重跑范围</h2>{run && <span>运行批次：{run.runId}</span>}</div>
    {!selected ? <p className="muted">选择步骤查看本轮真实数据；失败或被阻断的步骤不会显示旧数据。</p> : (
      <>
        <h3>{selected.name}</h3>
        {!result?.artifact ? <div className="banner danger">{result?.error ?? "当前没有本轮有效产物"}</div> : (
          <>
            <p>{result.artifact.summary} · 版本 {result.artifact.version}{result.reusedFrom ? ` · 复用 ${result.reusedFrom}` : ""}</p>
            {result.artifact.path && <p>输出文件：{result.artifact.path}</p>}
            <div className="table-wrap">
              <table>
                <thead><tr>{Object.keys(result.artifact.rows[0] ?? {}).map((key) => <th key={key}>{key}</th>)}</tr></thead>
                <tbody>{result.artifact.rows.slice(0, 8).map((row, index) => <tr key={index}>{Object.entries(row).map(([key, value]) => <td key={key}>{String(value)}</td>)}</tr>)}</tbody>
              </table>
            </div>
          </>
        )}
      </>
    )}
    {run && <div className="scope"><b>本轮范围：</b>{run.affectedNodeIds.map((id) => project.nodes.find((node) => node.id === id)?.name ?? id).join(" → ")}</div>}
    {run && <div className="reuse-report"><b>分支复用说明</b>
      {project.nodes.map((node) => {
        const stepResult = run.results[node.id];
        if (!stepResult) return null;
        return <div key={node.id} className={"reuse-row " + stepResult.status}><span>{node.name}</span><b>{statusText[stepResult.status] ?? stepResult.status}</b><em>{stepResult.reason ?? stepResult.error ?? ""}</em></div>;
      })}
    </div>}
  </section>;
}
