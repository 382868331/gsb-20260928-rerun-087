import { useEffect, useMemo, useState } from "react";
import type { DeliveryState, Project, PublishGroup, PublishGroupDependency } from "../types";

type Props = {
  project: Project;
  deliveries: Record<string, DeliveryState>;
  groups: Record<string, PublishGroup>;
  busy: boolean;
  onPrepare: (memberIds: string[], dependencies: PublishGroupDependency[]) => void;
  onCommit: (groupId: string, unavailable: string[]) => void;
  onRecover: (groupId: string) => void;
  onCancel: (groupId: string) => void;
};

const statusLabels: Record<PublishGroup["status"], string> = {
  prepared: "准备中（尚未提交，可整组取消）",
  committing: "提交中（中断后必须恢复到确定结局）",
  committed: "已完成（整组对接收方可见）",
  cancelled: "已取消（未发布任何结果）"
};

export function PublishGroupPanel({ project, deliveries, groups, busy, onPrepare, onCommit, onRecover, onCancel }: Props) {
  const publishNodes = project.nodes.filter((node) => node.type === "publish");
  const candidates = publishNodes.filter((node) => deliveries[node.id]?.pending && !deliveries[node.id].pending?.stale);
  const candidateKey = candidates.map((node) => node.id + "@" + deliveries[node.id].pending!.version).join(",");
  const [selected, setSelected] = useState<string[]>([]);
  const [dependencies, setDependencies] = useState<PublishGroupDependency[]>([]);
  const [outage, setOutage] = useState<Record<string, string>>({});
  useEffect(() => { setSelected(candidates.map((node) => node.id)); setDependencies([]); }, [candidateKey]);
  const groupList = useMemo(() => Object.values(groups).sort((a, b) => a.createdAt.localeCompare(b.createdAt)), [groups]);
  if (publishNodes.length === 0) return null;

  const receiverOf = (nodeId: string) => deliveries[nodeId]?.receiver || String(publishNodes.find((node) => node.id === nodeId)?.params.receiver ?? nodeId);
  const versionOf = (nodeId: string) => deliveries[nodeId]?.pending?.version ?? deliveries[nodeId]?.published?.version ?? "";
  const toggle = (nodeId: string) => setSelected((current) => current.includes(nodeId) ? current.filter((id) => id !== nodeId) : [...current, nodeId]);
  const patchDependency = (index: number, patch: Partial<PublishGroupDependency>) => setDependencies((current) => current.map((row, i) => {
    if (i !== index) return row;
    const next = { ...row, ...patch };
    if (patch.dependsOn !== undefined && !patch.version) next.version = versionOf(patch.dependsOn);
    return next;
  }));
  const publishedTargets = publishNodes.filter((node) => deliveries[node.id]?.published && !selected.includes(node.id)).map((node) => node.id);

  return <section className="publish-panel publish-group-panel">
    <div className="panel-title"><h2>发布组（多接收方一致发布事务）</h2>
      <button className="primary" disabled={busy || selected.length < 2} onClick={() => { onPrepare(selected, dependencies.filter((row) => row.nodeId && row.dependsOn && row.version)); setDependencies([]); }}>组成发布组（{selected.length} 个候选）</button>
    </div>
    {candidates.length === 0 && <p className="muted">当前没有可成组的待确认候选；运行后结果先是“仅已计算”，可在此组成发布组共同确认。</p>}
    {candidates.length > 0 && <div className="group-compose">
      <b>选择本次运行中要共同发布的候选（整组要么全部可见，要么全部不可见）：</b>
      {candidates.map((node) => {
        const pending = deliveries[node.id].pending!;
        return <label key={node.id} className="group-candidate">
          <input type="checkbox" checked={selected.includes(node.id)} onChange={() => toggle(node.id)} />
          <span>{receiverOf(node.id)} · 版本 {pending.version} · 来自 {pending.runId}</span>
        </label>;
      })}
      <div className="group-deps">
        <b>接收方版本依赖（可循环，例如 财务→运营→风控→财务）：</b>
        {dependencies.map((row, index) => <div key={index} className="group-dep-row">
          <select aria-label={"依赖方-" + index} value={row.nodeId} onChange={(event) => patchDependency(index, { nodeId: event.target.value })}>
            {selected.map((id) => <option key={id} value={id}>{receiverOf(id)}</option>)}
          </select>
          <span>依赖</span>
          <select aria-label={"被依赖方-" + index} value={row.dependsOn} onChange={(event) => patchDependency(index, { dependsOn: event.target.value })}>
            <option value="">选择接收方</option>
            {[...selected, ...publishedTargets].map((id) => <option key={id} value={id}>{receiverOf(id)}</option>)}
          </select>
          <span>的版本</span>
          <input aria-label={"依赖版本-" + index} value={row.version} onChange={(event) => patchDependency(index, { version: event.target.value })} />
          <button onClick={() => setDependencies((current) => current.filter((_, i) => i !== index))}>移除</button>
        </div>)}
        <button disabled={selected.length < 2} onClick={() => setDependencies((current) => [...current, { nodeId: selected[0], dependsOn: "", version: "" }])}>添加依赖</button>
      </div>
    </div>}
    {groupList.map((group) => <div key={group.id} className={"receiver-card group-card status-" + group.status}>
      <div className="receiver-head">
        <b>发布组 {group.id}</b>
        <span className={"delivery-state " + (group.status === "committed" ? "visible" : group.status === "cancelled" ? "stale" : "pending")}>{statusLabels[group.status]}</span>
      </div>
      <p className="delivery-line">成员：{group.members.map((member) => receiverOf(member.nodeId) + "（版本 " + member.version + "）").join("、")}</p>
      {group.dependencies.length > 0 && <p className="delivery-line">依赖：{group.dependencies.map((dep) => receiverOf(dep.nodeId) + " 依赖 " + receiverOf(dep.dependsOn) + " 的版本 " + dep.version).join("；")}</p>}
      {group.note && <p className="delivery-line">{group.note}</p>}
      {group.status === "committed" && group.adopted && <div className="group-adopted">
        <b>每个接收方实际采用的候选身份：</b>
        {Object.values(group.adopted).map((adoption) => <p key={adoption.nodeId} className="delivery-line visible-line">{receiverOf(adoption.nodeId)}：版本 {adoption.version} · 来自 {adoption.runId}</p>)}
      </div>}
      {group.status === "prepared" && <div className="publish-actions">
        <label>模拟接收方暂时不可用：
          <select aria-label={"模拟不可用-" + group.id} value={outage[group.id] ?? ""} onChange={(event) => setOutage((current) => ({ ...current, [group.id]: event.target.value }))}>
            <option value="">无</option>
            {group.members.map((member) => <option key={member.nodeId} value={member.nodeId}>{receiverOf(member.nodeId)}</option>)}
          </select>
        </label>
        <button className="primary" disabled={busy} onClick={() => onCommit(group.id, outage[group.id] ? [outage[group.id]] : [])}>提交整组</button>
        <button disabled={busy} onClick={() => onCancel(group.id)}>取消整组</button>
      </div>}
      {group.status === "committing" && <div className="publish-actions">
        <div className="banner danger">提交中断：接收方仍看到完整旧组；必须恢复到确定结局，不能取消。</div>
        <button className="primary" disabled={busy} onClick={() => onRecover(group.id)}>恢复提交</button>
      </div>}
    </div>)}
  </section>;
}

