import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import type { CandidateFilter, CandidateView, CompareView, Comparison } from "../types";

type Props = {
  projectId: string;
  // 运行 / 确认 / 重新打开后变化，触发候选与视图刷新。
  refreshKey: string;
};

const statusText: Record<string, string> = {
  pending: "待确认",
  published: "已发布",
  stale: "已失效",
  superseded: "被取代"
};

const emptyFilter: CandidateFilter = { receiver: "", projectVersion: "", runFrom: "", runTo: "", status: "" };

function cellText(value: unknown): string {
  if (value === undefined) return "（缺失）";
  if (value === null) return "null";
  return typeof value === "object" ? JSON.stringify(value) : String(value);
}

export function ComparePanel({ projectId, refreshKey }: Props) {
  const [filter, setFilter] = useState<CandidateFilter>(emptyFilter);
  const [candidates, setCandidates] = useState<CandidateView[]>([]);
  const [views, setViews] = useState<CompareView[]>([]);
  const [viewName, setViewName] = useState("");
  const [diffOnly, setDiffOnly] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [comparison, setComparison] = useState<Comparison>();
  const [message, setMessage] = useState("");

  const reload = useCallback(async (activeFilter: CandidateFilter) => {
    try {
      const [candidateResponse, viewResponse] = await Promise.all([
        api.candidates(projectId, activeFilter),
        api.compareViews(projectId)
      ]);
      setCandidates(candidateResponse.candidates ?? []);
      setViews(viewResponse.views ?? []);
      setMessage("");
    } catch {
      // 工程尚未保存过（404）时静默：还没有可对比的候选。
      setCandidates([]);
    }
  }, [projectId]);

  useEffect(() => { void reload(filter); }, [reload, filter, refreshKey]);

  const receivers = useMemo(() => [...new Set(candidates.map((candidate) => candidate.receiver))].sort(), [candidates]);
  const revisions = useMemo(() => [...new Set(candidates.map((candidate) => candidate.projectVersion))].sort(), [candidates]);

  function patchFilter(patch: Partial<CandidateFilter>) {
    setFilter((current) => ({ ...current, ...patch }));
  }

  function toggleSelect(key: string) {
    setSelected((current) => {
      if (current.includes(key)) return current.filter((item) => item !== key);
      return [...current.slice(-1), key];
    });
  }

  async function compare() {
    if (selected.length !== 2) return;
    try {
      const result = await api.compare(projectId, selected[0], selected[1]);
      setComparison(result);
      setMessage("");
    } catch (error) {
      setMessage("对比失败：" + (error instanceof Error ? error.message : "后端不可用"));
    }
  }

  async function saveView() {
    const name = viewName.trim();
    if (!name) {
      setMessage("请先输入视图名称再保存筛选条件。");
      return;
    }
    try {
      const response = await api.saveCompareView(projectId, { name, filter, diffOnly });
      setViews(response.views ?? []);
      setMessage("筛选视图「" + name + "」已保存，重开工程后仍可继续核对。");
    } catch (error) {
      setMessage("保存视图失败：" + (error instanceof Error ? error.message : "后端不可用"));
    }
  }

  function applyView(name: string) {
    const view = views.find((item) => item.name === name);
    if (!view) return;
    setViewName(view.name);
    setDiffOnly(view.diffOnly);
    setFilter({ ...emptyFilter, ...view.filter });
  }

  async function removeView() {
    const name = viewName.trim();
    if (!name || !views.some((view) => view.name === name)) return;
    try {
      const response = await api.deleteCompareView(projectId, name);
      setViews(response.views ?? []);
      setViewName("");
    } catch { /* 保留本地状态 */ }
  }

  function exportComparison() {
    if (!comparison) return;
    const payload = {
      exportedAt: new Date().toISOString(),
      projectId,
      filter,
      diffOnly,
      comparison
    };
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "candidate-compare-" + comparison.a.runId + "-vs-" + comparison.b.runId + ".json";
    anchor.click();
    URL.revokeObjectURL(url);
  }

  const basisRows = comparison ? [
    { field: "接收方", a: comparison.a.receiver, b: comparison.b.receiver },
    { field: "来源修订", a: comparison.a.projectVersion, b: comparison.b.projectVersion },
    { field: "运行批次", a: comparison.a.runId, b: comparison.b.runId },
    { field: "输出版本", a: comparison.a.version, b: comparison.b.version },
    { field: "发布结果", a: statusText[comparison.a.status] ?? comparison.a.status, b: statusText[comparison.b.status] ?? comparison.b.status }
  ] : [];
  const visibleBasisRows = diffOnly ? basisRows.filter((row) => row.a !== row.b) : basisRows;

  return <section className="compare-panel">
    <div className="panel-title">
      <h2>跨运行候选对比</h2>
      <label className="diff-only"><input type="checkbox" checked={diffOnly} onChange={(event) => setDiffOnly(event.target.checked)} />只看差异</label>
    </div>
    <div className="compare-filters">
      <select value={filter.receiver ?? ""} onChange={(event) => patchFilter({ receiver: event.target.value })}>
        <option value="">全部接收方</option>
        {receivers.map((receiver) => <option key={receiver} value={receiver}>{receiver}</option>)}
      </select>
      <select value={filter.projectVersion ?? ""} onChange={(event) => patchFilter({ projectVersion: event.target.value })}>
        <option value="">全部来源修订</option>
        {revisions.map((revision) => <option key={revision} value={revision}>{revision.slice(0, 12)}</option>)}
      </select>
      <input placeholder="运行时间起 run-…" value={filter.runFrom ?? ""} onChange={(event) => patchFilter({ runFrom: event.target.value })} />
      <input placeholder="运行时间止 run-…" value={filter.runTo ?? ""} onChange={(event) => patchFilter({ runTo: event.target.value })} />
      <select value={filter.status ?? ""} onChange={(event) => patchFilter({ status: event.target.value })}>
        <option value="">全部发布结果</option>
        {Object.entries(statusText).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select>
    </div>
    <div className="compare-views">
      <select value="" onChange={(event) => event.target.value && applyView(event.target.value)}>
        <option value="">打开已保存视图…</option>
        {views.map((view) => <option key={view.name} value={view.name}>{view.name}</option>)}
      </select>
      <input placeholder="视图名称" value={viewName} onChange={(event) => setViewName(event.target.value)} />
      <button onClick={saveView}>保存筛选条件</button>
      {views.some((view) => view.name === viewName.trim()) && <button onClick={removeView}>删除视图</button>}
    </div>
    {message && <div className="banner warning">{message}</div>}
    {candidates.length === 0 ? <p className="muted">当前筛选条件下没有候选；运行发布后，每次计算的候选都会跨运行归档在这里。</p> : (
      <div className="table-wrap candidate-table">
        <table>
          <thead><tr><th></th><th>接收方</th><th>运行批次</th><th>来源修订</th><th>输出版本</th><th>行数</th><th>发布结果</th></tr></thead>
          <tbody>{candidates.map((candidate) => <tr key={candidate.key} className={selected.includes(candidate.key) ? "selected" : ""}>
            <td><input type="checkbox" checked={selected.includes(candidate.key)} onChange={() => toggleSelect(candidate.key)} /></td>
            <td>{candidate.receiver}</td>
            <td>{candidate.runId}</td>
            <td title={candidate.projectVersion}>{candidate.projectVersion.slice(0, 12)}</td>
            <td title={candidate.version}>{candidate.version.slice(0, 12)}</td>
            <td>{candidate.empty ? "空表" : candidate.rows}</td>
            <td><span className={"delivery-state " + (candidate.status === "published" ? "visible" : candidate.status === "pending" ? "pending" : "stale")}>{statusText[candidate.status] ?? candidate.status}</span></td>
          </tr>)}</tbody>
        </table>
      </div>
    )}
    <div className="compare-actions">
      <button className="primary" disabled={selected.length !== 2} onClick={compare}>对比选中的两个候选</button>
      {comparison && <button onClick={exportComparison}>导出对比结果</button>}
    </div>
    {comparison && <div className="comparison-result">
      {comparison.identical
        ? <div className="banner warning">两个候选的输出记录完全相同，但计算依据不同（见下方依据差异），仍为两条独立候选，不合并。</div>
        : <div className="banner">输出差异：新增 {comparison.added.length} 行 · 删除 {comparison.removed.length} 行 · 字段变化 {comparison.changes.length} 行。</div>}
      {visibleBasisRows.length > 0 && <div className="table-wrap">
        <table>
          <thead><tr><th>计算依据</th><th>候选 A</th><th>候选 B</th></tr></thead>
          <tbody>{visibleBasisRows.map((row) => <tr key={row.field} className={row.a !== row.b ? "basis-diff" : ""}>
            <td>{row.field}</td><td>{row.a}</td><td>{row.b}</td>
          </tr>)}</tbody>
        </table>
      </div>}
      {comparison.added.length > 0 && <div className="diff-block added"><b>新增记录（仅在 B）</b>{comparison.added.map((row, index) => <pre key={index}>{JSON.stringify(row)}</pre>)}</div>}
      {comparison.removed.length > 0 && <div className="diff-block removed"><b>删除记录（仅在 A）</b>{comparison.removed.map((row, index) => <pre key={index}>{JSON.stringify(row)}</pre>)}</div>}
      {comparison.changes.length > 0 && <div className="diff-block changed"><b>字段变化</b>
        <table>
          <thead><tr><th>行</th><th>字段</th><th>候选 A</th><th>候选 B</th></tr></thead>
          <tbody>{comparison.changes.flatMap((change) => change.fields.map((field, index) => <tr key={change.rowKey + field.field + index}>
            <td>{change.rowKey}</td><td>{field.field}</td><td>{cellText(field.a)}</td><td>{cellText(field.b)}</td>
          </tr>))}</tbody>
        </table>
      </div>}
    </div>}
  </section>;
}

