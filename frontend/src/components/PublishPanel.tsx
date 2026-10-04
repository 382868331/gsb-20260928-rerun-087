import type { ConfirmTarget, Delivery, DeliveryEventKind, DeliveryState, Project, RunResult } from "../types";

type Props = {
  project: Project;
  run?: RunResult;
  deliveries: Record<string, DeliveryState>;
  confirming: boolean;
  onConfirmAll: (targets: ConfirmTarget[]) => void;
  onConfirmOne: (target: ConfirmTarget) => void;
};

const eventLabels: Record<DeliveryEventKind, string> = {
  computed: "已计算",
  superseded: "被取代",
  published: "已发布",
  invalidated: "已失效",
  rejected: "拒绝确认"
};

function targetOf(delivery: Delivery): ConfirmTarget {
  return { nodeId: delivery.nodeId, version: delivery.version, runId: delivery.runId };
}

function deliveryLabel(state: DeliveryState): string {
  if (state.pending?.stale && state.published) return "新候选已失效，接收方仍看到已发布版本";
  if (state.pending?.stale) return "待确认候选已失效，需重新运行";
  if (state.pending && state.published) return "新版本仅已计算，接收方仍看到已发布版本";
  if (state.pending) return "仅已计算，对接收方不可见";
  if (state.published) return "已对接收方可见";
  return "尚未产生投递";
}

function describeDelivery(prefix: string, delivery: NonNullable<DeliveryState["pending"]>): string {
  const rows = delivery.empty ? "空表（0 行）" : delivery.rows + " 行";
  return prefix + "：版本 " + delivery.version + " · " + rows + " · 来自 " + delivery.runId;
}

export function PublishPanel({ project, run, deliveries, confirming, onConfirmAll, onConfirmOne }: Props) {
  const publishNodes = project.nodes.filter((node) => node.type === "publish");
  if (publishNodes.length === 0) return null;
  const states = publishNodes.map((node) => deliveries[node.id] ?? { nodeId: node.id, receiver: String(node.params.receiver ?? ""), history: [] });
  const pendingStates = states.filter((state) => state.pending && !state.pending.stale);
  const pendingCount = pendingStates.length;
  const failed = run && !run.ok;
  return <section className="publish-panel">
    <div className="panel-title">
      <h2>接收方发布核对</h2>
      <div className="publish-actions">
        {pendingCount > 1 && <button className="primary" disabled={confirming} onClick={() => onConfirmAll(pendingStates.map((state) => targetOf(state.pending!)))}>全部结果共同确认（{pendingCount}）</button>}
      </div>
    </div>
    {failed && <div className="banner danger">本轮有分支失败：下面标明每个接收方的结果仅已计算还是已经可见；已确认部分不会被重跑重复发布。</div>}
    {states.map((state) => {
      const node = publishNodes.find((item) => item.id === state.nodeId)!;
      const nodeResult = run?.results[state.nodeId];
      return <div key={state.nodeId} className="receiver-card">
        <div className="receiver-head">
          <b>{state.receiver || node.name}</b>
          <span className={"delivery-state" + (state.pending?.stale ? " stale" : state.published && !state.pending ? " visible" : state.pending ? " pending" : "")}>{deliveryLabel(state)}</span>
        </div>
        {nodeResult && (nodeResult.status === "failed" || nodeResult.status === "blocked") && <div className="banner danger">分支{nodeResult.status === "failed" ? "失败" : "被阻断"}：{nodeResult.error ?? "本轮没有新结果"}</div>}
        {state.published && <p className="delivery-line visible-line">{describeDelivery("接收方当前可见", state.published)}</p>}
        {state.pending && !state.pending.stale && <p className="delivery-line pending-line">{describeDelivery("待确认（仅已计算）", state.pending)}</p>}
        {state.pending?.stale && <p className="delivery-line stale-line">{describeDelivery("已失效候选（仅供查阅差异，不能确认）", state.pending)}</p>}
        {state.pending?.stale && <div className="banner danger">确认被拒绝：{state.pending.staleReason || "候选失去依据"}。需重新运行产生新候选。</div>}
        {!state.published && !state.pending && <p className="muted">该接收方还没有任何投递记录。</p>}
        {state.pending && !state.pending.stale && <button disabled={confirming} onClick={() => onConfirmOne(targetOf(state.pending))}>允许该结果先行确认</button>}
        {(state.invalidated?.length ?? 0) > 0 && <details className="evidence"><summary>已拒绝/失效候选（{state.invalidated!.length} 个，仅供比较，恢复原文不能复活）</summary>
          {state.invalidated!.map((delivery) => <div key={delivery.version + delivery.runId} className="evidence-row">
            <b>已失效候选</b>
            <span>{describeDelivery("版本", delivery)}</span>
            <em>{delivery.staleReason || "候选失去依据"}</em>
          </div>)}
        </details>}
        {state.history.length > 0 && <details className="evidence"><summary>投递证据（{state.history.length} 条，撤回修订不会删除）</summary>
          {state.history.map((event, index) => <div key={index} className="evidence-row">
            <b>{eventLabels[event.kind] ?? event.kind}</b>
            <span>版本 {event.version} · {event.runId}</span>
            <em>{event.note}</em>
          </div>)}
        </details>}
      </div>;
    })}
  </section>;
}
