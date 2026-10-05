import { useMemo, useState } from "react";
import type { DeliveryState, Project, PublishGroup, PublishGroupDependency } from "../types";

type Props = {
  project: Project;
  deliveries: Record<string, DeliveryState>;
  groups: PublishGroup[];
  receiverDown: Record<string, boolean>;
  busy: boolean;
  onCreate: (nodeIds: string[], dependencies: PublishGroupDependency[]) => void;
  onConfirm: (groupId: string) => void;
  onCancel: (groupId: string) => void;
  onToggleReceiver: (receiver: string, available: boolean) => void;
};

const statusText: Record<string, string> = {
  prepared: "准备中（可取消，未发布）",
  committing: "提交中（需恢复到确定结局）",
  committed: "已完成（整组对接收方可见）",
  aborted: "已中止（整组未发布）",
  cancelled: "已取消（整组未发布）"
};

export function PublishGroupPanel({ project, deliveries, groups, receiverDown, busy, onCreate, onConfirm, onCancel, onToggleReceiver }: Props) {
  const publishNodes = project.nodes.filter((node) => node.type === "publish");
  const candidates = publishNodes
    .map((node) => deliveries[node.id])
    .filter((state): state is DeliveryState => !!state && !!state.pending && !state.pending.stale);
  const [selected, setSelected] = useState<string[]>([]);
  const [deps, setDeps] = useState<PublishGroupDependency[]>([]);
  const receivers = useMemo(() => [...new Set(publishNodes.map((node) => String(node.params.receiver ?? "")).filter(Boolean))], [project.nodes]);
  if (publishNodes.length === 0) return null;
  const selectedReceivers = candidates.filter((state) => selected.includes(state.nodeId)).map((state) => state.receiver);

  const toggle = (nodeId: string) => setSelected((current) => current.includes(nodeId) ? current.filter((id) => id !== nodeId) : [...current, nodeId]);
  const patchDep = (index: number, patch: Partial<PublishGroupDependency>) => setDeps((current) => current.map((dep, i) => i === index ? { ...dep, ...patch } : dep));

  return <section className="publish-panel group-panel">
    <div className="panel-title"><h2>发布组（多接收方一致发布）</h2></div>
    <p className="muted">把一次运行中的若干候选组成发布组并声明接收方之间的版本依赖；确认前共同校验依据与冲突，任一不满足整组保持未发布。</p>

    <div className="receiver-card">
      <b>组成发布组</b>
      {candidates.length === 0 && <p className="muted">当前没有可组队的待确认候选，请先运行。</p>}
      {candidates.map((state) => <label key={state.nodeId} className="candidate-pick">
        <input type="checkbox" checked={selected.includes(state.nodeId)} onChange={() => toggle(state.nodeId)} />
        {state.receiver} · 版本 {state.pending!.version} · 来自 {state.pending!.runId}
      </label>)}
      {selected.length >= 2 && <div className="dep-editor">
        <b>接收方版本依赖</b>
        {deps.map((dep, index) => <div key={index} className="dep-row">
          <select aria-label={"依赖方-" + index} value={dep.receiver} onChange={(event) => patchDep(index, { receiver: event.target.value })}>
            <option value="">选择接收方</option>
            {selectedReceivers.map((receiver) => <option key={receiver} value={receiver}>{receiver}</option>)}
          </select>
          依赖
          <select aria-label={"被依赖方-" + index} value={dep.onReceiver} onChange={(event) => patchDep(index, { onReceiver: event.target.value })}>
            <option value="">选择接收方</option>
            {[...new Set([...selectedReceivers, ...receivers])].map((receiver) => <option key={receiver} value={receiver}>{receiver}</option>)}
          </select>
          版本
          <input aria-label={"依赖版本-" + index} value={dep.version} onChange={(event) => patchDep(index, { version: event.target.value })} placeholder="要求版本" />
          <button onClick={() => setDeps((current) => current.filter((_, i) => i !== index))}>删除</button>
        </div>)}
        <button onClick={() => setDeps((current) => [...current, { receiver: selectedReceivers[0] ?? "", onReceiver: "", version: "" }])}>添加依赖</button>
      </div>}
      <button className="primary" disabled={busy || selected.length < 2} onClick={() => { onCreate(selected, deps.filter((dep) => dep.receiver && dep.onReceiver)); }}>组成发布组（{selected.length} 个候选）</button>
    </div>

    {receivers.length > 0 && <div className="receiver-card">
      <b>接收方可用性（验证提交中断开与恢复）</b>
      {receivers.map((receiver) => <label key={receiver} className="candidate-pick">
        <input type="checkbox" checked={!!receiverDown[receiver]} onChange={(event) => onToggleReceiver(receiver, !event.target.checked)} />
        模拟 {receiver} 暂时不可用
      </label>)}
    </div>}

    {groups.map((group) => <div key={group.id} className="receiver-card">
      <div className="receiver-head">
        <b>发布组 {group.id}</b>
        <span className={"delivery-state group-" + group.status}>{statusText[group.status] ?? group.status}</span>
      </div>
      {group.members.map((member) => <p key={member.nodeId} className="delivery-line">
        {member.receiver}：候选版本 {member.version}（{member.runId}），建组前提可见版本 {member.baseVersion || "（无）"}
      </p>)}
      {group.dependencies.map((dep, index) => <p key={index} className="delivery-line">依赖：{dep.receiver} 需要 {dep.onReceiver} 处于版本 {dep.version}</p>)}
      {group.outcomes && group.outcomes.length > 0 && <p className="delivery-line visible-line">实际采用：{group.outcomes.map((outcome) => outcome.receiver + " ← " + outcome.version).join("；")}</p>}
      {group.statusNote && <div className="banner danger">{group.statusNote}</div>}
      <div className="publish-actions">
        {group.status === "prepared" && <>
          <button className="primary" disabled={busy} onClick={() => onConfirm(group.id)}>确认发布整组</button>
          <button disabled={busy} onClick={() => onCancel(group.id)}>取消发布组</button>
        </>}
        {group.status === "committing" && <button className="primary" disabled={busy} onClick={() => onConfirm(group.id)}>恢复提交</button>}
      </div>
    </div>)}
  </section>;
}
