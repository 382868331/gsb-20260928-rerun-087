import { useEffect, useMemo, useState } from "react";
import type { CandidateArchive, CandidateComparisonFilters, CandidateComparisonState, CandidateInputRevision } from "../types";

type Props = {
  archives: CandidateArchive[];
  savedState?: CandidateComparisonState;
  onSave: (state: CandidateComparisonState) => Promise<void>;
};

type Row = Record<string, unknown>;
type RowDiff = { key: string; status: "added" | "removed" | "changed" | "same"; left?: Row; right?: Row; changedFields: string[] };
type RevisionDiff = { nodeId: string; nodeName: string; left?: CandidateInputRevision; right?: CandidateInputRevision; rows: RowDiff[] };

const emptyFilters: CandidateComparisonFilters = { receiver: "", projectRevision: "", runId: "", result: "" };
const statusText: Record<string, string> = { pending: "待确认", pending_same: "待确认（输出同当前发布）", published: "已发布", published_same: "已发布版本一致", superseded: "已被取代", stale: "已失效" };

function stableValue(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function canonical(row: Row): string {
  return JSON.stringify(Object.fromEntries(Object.entries(row).sort(([a], [b]) => a.localeCompare(b))));
}

function canonicalRows(rows: Row[]): string {
  return JSON.stringify(rows.map(canonical));
}

function rowIdentity(row: Row, index: number): string {
  const preferred = ["id", "ID", "Id", "_id", "code", "key"];
  for (const field of preferred) {
    if (row[field] !== undefined && stableValue(row[field]) !== "") return field + "=" + stableValue(row[field]);
  }
  return "row#" + index + ":" + canonical(row);
}

function compareRows(leftRows: Row[] = [], rightRows: Row[] = []): RowDiff[] {
  const diffs: RowDiff[] = [];
  const rightUsed = new Set<number>();
  leftRows.forEach((left, leftIndex) => {
    const key = rowIdentity(left, leftIndex);
    const rightIndex = rightRows.findIndex((right, index) => !rightUsed.has(index) && rowIdentity(right, index) === key);
    if (rightIndex < 0) {
      diffs.push({ key, status: "removed", left, changedFields: [] });
      return;
    }
    rightUsed.add(rightIndex);
    const right = rightRows[rightIndex];
    const changedFields = [...new Set([...Object.keys(left), ...Object.keys(right)])].filter((field) => stableValue(left[field]) !== stableValue(right[field])).sort();
    diffs.push({ key, status: changedFields.length ? "changed" : "same", left, right, changedFields });
  });
  rightRows.forEach((right, index) => {
    if (!rightUsed.has(index)) diffs.push({ key: rowIdentity(right, index), status: "added", right, changedFields: [] });
  });
  return diffs.sort((a, b) => a.key.localeCompare(b.key));
}

function inputDiffs(left?: CandidateArchive, right?: CandidateArchive): RevisionDiff[] {
  const byNode = new Map<string, RevisionDiff>();
  left?.inputRevisions.forEach((revision) => byNode.set(revision.nodeId, { nodeId: revision.nodeId, nodeName: revision.nodeName, left: revision, rows: [] }));
  right?.inputRevisions.forEach((revision) => {
    const current = byNode.get(revision.nodeId) ?? { nodeId: revision.nodeId, nodeName: revision.nodeName, rows: [] };
    current.right = revision;
    byNode.set(revision.nodeId, current);
  });
  return [...byNode.values()].sort((a, b) => a.nodeId.localeCompare(b.nodeId)).map((item) => ({ ...item, rows: compareRows(item.left?.rows ?? [], item.right?.rows ?? []) }));
}

function hasChanges(diffs: RowDiff[]): boolean {
  return diffs.some((diff) => diff.status !== "same");
}

function basisText(candidate: CandidateArchive): string {
  const inputs = candidate.inputRevisions.map((item) => item.nodeName + " " + item.version).join(", ") || "无输入端口";
  return "修订 " + candidate.projectRevision + "（" + candidate.projectVersion.slice(0, 8) + "） · 运行 " + candidate.runId + " · 输入：" + inputs;
}

function short(value: string): string {
  return value.length > 12 ? value.slice(0, 12) + "…" : value;
}

function DiffTable({ diffs, onlyDiff }: { diffs: RowDiff[]; onlyDiff: boolean }) {
  const visible = onlyDiff ? diffs.filter((diff) => diff.status !== "same") : diffs;
  if (!visible.length) return <p className="muted">没有差异。</p>;
  return <div className="comparison-table"><table><thead><tr><th>状态</th><th>记录</th><th>字段变化</th></tr></thead><tbody>
    {visible.map((diff) => <tr key={diff.key} className={"diff-" + diff.status}>
      <td>{{ added: "右侧新增", removed: "右侧删除", changed: "字段变化", same: "相同" }[diff.status]}</td>
      <td><code>{diff.key}</code>{diff.changedFields.length > 0 && <div className="field-values"><span>{short(stableValue(diff.left?.[diff.changedFields[0]]))}</span><b>→</b><span>{short(stableValue(diff.right?.[diff.changedFields[0]]))}</span></div>}</td>
      <td>{diff.changedFields.join(", ") || "—"}</td>
    </tr>)}
  </tbody></table></div>;
}

export function CandidateComparison({ archives, savedState, onSave }: Props) {
  const [filters, setFilters] = useState<CandidateComparisonFilters>(savedState?.filters ?? emptyFilters);
  const [leftKey, setLeftKey] = useState(savedState?.leftKey ?? "");
  const [rightKey, setRightKey] = useState(savedState?.rightKey ?? "");
  const [onlyDiff, setOnlyDiff] = useState(savedState?.onlyDiff ?? false);
  const [saveMessage, setSaveMessage] = useState("");

  useEffect(() => {
    if (!savedState) return;
    setFilters(savedState.filters ?? emptyFilters);
    setLeftKey(savedState.leftKey);
    setRightKey(savedState.rightKey);
    setOnlyDiff(savedState.onlyDiff);
  }, [savedState]);

  const receivers = useMemo(() => [...new Set(archives.map((candidate) => candidate.receiver))].sort(), [archives]);
  const revisions = useMemo(() => [...new Set(archives.map((candidate) => candidate.projectRevision))].sort((a, b) => a - b), [archives]);
  const runs = useMemo(() => [...new Set(archives.map((candidate) => candidate.runId))].sort(), [archives]);
  const filtered = useMemo(() => archives.filter((candidate) =>
    (!filters.receiver || candidate.receiver === filters.receiver) &&
    (!filters.projectRevision || candidate.projectRevision === Number(filters.projectRevision)) &&
    (!filters.runId || candidate.runId === filters.runId) &&
    (!filters.result || candidate.status === filters.result)
  ), [archives, filters]);
  const left = archives.find((candidate) => candidate.key === leftKey);
  const right = archives.find((candidate) => candidate.key === rightKey);
  const outputDiffs = useMemo(() => left && right ? compareRows(left.outputRows, right.outputRows) : [], [left, right]);
  const revisionsDiff = useMemo(() => left && right ? inputDiffs(left, right) : [], [left, right]);
  const outputsEqual = Boolean(left && right && canonicalRows(left.outputRows) === canonicalRows(right.outputRows));
  const basisSignature = (candidate: CandidateArchive) => JSON.stringify([candidate.projectVersion, candidate.runId, candidate.inputRevisions.map((item) => [item.nodeId, item.version])]);
  const basisDifferent = Boolean(left && right && basisSignature(left) !== basisSignature(right));

  function updateFilter(patch: Partial<CandidateComparisonFilters>) {
    setFilters((current) => ({ ...current, ...patch }));
    setSaveMessage("");
  }
  async function save() {
    if (!left || !right) return;
    await onSave({ filters, leftKey, rightKey, onlyDiff });
    setSaveMessage("筛选与核对选择已随工程保存，重开后可继续。");
  }
  function exportResult() {
    if (!left || !right) return;
    const payload = { exportedAt: new Date().toISOString(), filters, left, right, outputDiffs, inputDiffs: revisionsDiff, outputsEqual, basisDifferent };
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "candidate-comparison-" + left.runId + "-" + right.runId + ".json";
    link.click();
    URL.revokeObjectURL(url);
  }

  return <section className="candidate-comparison">
    <div className="panel-title"><h2>跨运行候选对比（只读）</h2><span>{archives.length} 个候选档案</span></div>
    <p className="muted">对比只核对档案，不会确认、发布或恢复候选；接收方当前可见版本保持不变。</p>
    <div className="comparison-filters">
      <select value={filters.receiver} onChange={(event) => updateFilter({ receiver: event.target.value })}><option value="">全部接收方</option>{receivers.map((receiver) => <option key={receiver} value={receiver}>{receiver}</option>)}</select>
      <select value={filters.projectRevision} onChange={(event) => updateFilter({ projectRevision: event.target.value })}><option value="">全部来源修订</option>{revisions.map((revision) => <option key={revision} value={revision}>revision {revision}</option>)}</select>
      <select value={filters.runId} onChange={(event) => updateFilter({ runId: event.target.value })}><option value="">全部运行时间</option>{runs.map((run) => <option key={run} value={run}>{run}</option>)}</select>
      <select value={filters.result} onChange={(event) => updateFilter({ result: event.target.value })}><option value="">全部发布结果</option>{Object.entries(statusText).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
    </div>
    <div className="comparison-selects">
      <select value={leftKey} onChange={(event) => setLeftKey(event.target.value)}><option value="">选择候选 A</option>{filtered.map((candidate) => <option key={candidate.key} value={candidate.key}>{candidate.receiver} · {candidate.runId} · {statusText[candidate.status]}</option>)}</select>
      <select value={rightKey} onChange={(event) => setRightKey(event.target.value)}><option value="">选择候选 B</option>{filtered.map((candidate) => <option key={candidate.key} value={candidate.key}>{candidate.receiver} · {candidate.runId} · {statusText[candidate.status]}</option>)}</select>
      <label className="only-diff"><input type="checkbox" checked={onlyDiff} onChange={(event) => setOnlyDiff(event.target.checked)} />只看差异</label>
      <button disabled={!left || !right} onClick={save}>保存筛选条件</button>
      <button disabled={!left || !right} onClick={exportResult}>导出对比结果</button>
    </div>
    {saveMessage && <div className="banner comparison-saved">{saveMessage}</div>}
    {!left || !right ? <p className="muted">请选择任意两个候选开始并排核对。相同输出但依据不同的候选会保留为两条独立记录。</p> : <>
      {outputsEqual && basisDifferent && <div className="banner warning basis-warning">输出记录完全相同，但计算依据不同：以下依据差异明确保留，两个候选未合并。</div>}
      <div className="candidate-side-grid">{[left, right].map((candidate) => <article key={candidate.key} className="candidate-side">
        <h3>{candidate.receiver} · {candidate.nodeName}</h3>
        <span className={"delivery-state " + candidate.status}>{statusText[candidate.status]}</span>
        <p>{basisText(candidate)}</p>
        <p>输出版本 {candidate.outputVersion} · {candidate.empty ? "空表（0 行）" : candidate.rows + " 行"}</p>
        {candidate.status === "stale" && <b>该候选已失效，不能恢复发布资格。</b>}
      </article>)}</div>
      <div className="comparison-sections">
        <div><h3>输出记录差异</h3><div className="diff-summary">新增 {outputDiffs.filter((item) => item.status === "added").length} · 删除 {outputDiffs.filter((item) => item.status === "removed").length} · 字段变化 {outputDiffs.filter((item) => item.status === "changed").length}</div><DiffTable diffs={outputDiffs} onlyDiff={onlyDiff} /></div>
        <div><h3>输入修订差异</h3>{revisionsDiff.map((revision) => {
          const changed = hasChanges(revision.rows) || revision.left?.version !== revision.right?.version || !revision.left || !revision.right;
          if (onlyDiff && !changed) return null;
          return <div key={revision.nodeId} className="revision-diff"><b>{revision.nodeName}</b><span>{revision.left?.version ?? "缺失"} → {revision.right?.version ?? "缺失"}</span><DiffTable diffs={revision.rows} onlyDiff={onlyDiff} /></div>;
        })}</div>
      </div>
    </>}
  </section>;
}
