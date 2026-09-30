import type { Project, RunResult } from "../types";

type Props = { project: Project; selectedId?: string; run?: RunResult; projectVersion?: string };

export function DataPanel({ project, selectedId, run, projectVersion }: Props) {
  const selected = project.nodes.find((node) => node.id === selectedId);
  const result = selectedId ? run?.results[selectedId] : undefined;
  const recomputed = run?.affectedNodeIds ?? [];
  const reused = run ? Object.values(run.results).filter((item) => item.status === "reused") : [];
  const failedOrBlocked = run ? Object.values(run.results).filter((item) => item.status === "failed" || item.status === "blocked") : [];
  const versionMismatch = !!run?.projectVersion && !!projectVersion && run.projectVersion !== projectVersion;

  return <section className="data-panel">
    <div className="panel-title"><h2>实际数据与复用说明</h2>{run && <span>运行批次：{run.runId}</span>}</div>
    {run && (
      <div className="run-explain">
        <div><b>本轮重算（{recomputed.length}）：</b>{recomputed.length ? recomputed.map((id) => project.nodes.find((node) => node.id === id)?.name ?? id).join(" → ") : "无（打开/复用场景）"}</div>
        <div><b>沿用上一轮产物（{reused.length}）：</b>{reused.length ? reused.map((item) => (project.nodes.find((node) => node.id === item.nodeId)?.name ?? item.nodeId) + "←" + item.reusedFrom).join("；") : "无"}</div>
        {failedOrBlocked.length > 0 && <div className="banner danger"><b>失败或阻断（{failedOrBlocked.length}）：</b>{failedOrBlocked.map((item) => (project.nodes.find((node) => node.id === item.nodeId)?.name ?? item.nodeId) + "：" + (item.error ?? "")).join("；")}</div>}
        {versionMismatch && <div className="banner warning">页面工程版本与最后一次运行版本不一致：显示的旧产物已标记失效，重新运行后才会更新。</div>}
      </div>
    )}
    {!selected ? <p className="muted">选择步骤查看本轮真实数据；失败或被阻断的步骤不会显示旧数据冒充本轮结果。</p> : (
      <>
        <h3>{selected.name}</h3>
        {!result?.artifact ? <div className="banner danger">{result?.error ?? "当前没有本轮有效产物"}{result?.basis ? <div className="muted">依据：{result.basis}</div> : null}</div> : (
          <>
            <p>{result.artifact.summary} · 节点版本 {result.artifact.version}{result.reusedFrom ? ` · 沿用 ${result.reusedFrom} 的产物` : " · 本轮新产物"}</p>
            {result.basis && <p className="muted">依据：{result.basis}</p>}
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
  </section>;
}
