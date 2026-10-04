import { useEffect, useMemo, useState } from "react";
import { Background, Controls, ReactFlow, addEdge, useEdgesState, useNodesState, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "./api";
import { blankProject, createNode, downstreamIds, emptyProject, normalizeProject, toReactEdge } from "./project";
import { DataPanel } from "./components/DataPanel";
import { ComparePanel } from "./components/ComparePanel";
import { Inspector } from "./components/Inspector";
import { PublishPanel } from "./components/PublishPanel";
import { StepNode, type StepNodeData } from "./components/StepNode";
import type { ComponentDef, DeliveryState, Issue, Project, RunResult, Validation } from "./types";

const nodeTypes = { step: StepNode };

// 后端对没有端口的组件会返回 []（旧数据里也可能出现 null/undefined），
// 统一归一化为数组，避免空端口列表被当成普通列表 map 时整页崩溃。
function withEmptyPorts(component: ComponentDef | undefined): ComponentDef | undefined {
  if (!component) return component;
  return { ...component, inputs: component.inputs ?? [], outputs: component.outputs ?? [], params: component.params ?? [] };
}

export default function App() {
  const [components, setComponents] = useState<ComponentDef[]>([]);
  const [project, setProject] = useState<Project>(emptyProject);
  const [selectedId, setSelectedId] = useState<string | undefined>("filter-1");
  const [validation, setValidation] = useState<Validation>();
  const [run, setRun] = useState<RunResult>();
  const [deliveries, setDeliveries] = useState<Record<string, DeliveryState>>({});
  const [confirming, setConfirming] = useState(false);
  const [dirtyIds, setDirtyIds] = useState<Set<string>>(new Set());
  const staleIds = useMemo(() => downstreamIds(project, [...dirtyIds]), [project, dirtyIds]);
  const [savedSnapshot, setSavedSnapshot] = useState<{ project: Project; run?: RunResult }>();
  const [message, setMessage] = useState("先保存，再执行；空工程与空端口节点也能直接进入编辑。");
  const isPublishedDirty = useMemo(() => {
    if (!savedSnapshot) return false;
    return JSON.stringify({ nodes: project.nodes, edges: project.edges }) !== JSON.stringify({ nodes: savedSnapshot.project.nodes, edges: savedSnapshot.project.edges });
  }, [project, savedSnapshot]);
  const flowNodes = useMemo(() => project.nodes.map((node) => ({
    id: node.id,
    type: "step",
    position: node.position,
    data: {
      node,
      component: withEmptyPorts(components.find((item) => item.type === node.type)),
      result: run?.results[node.id],
      stale: staleIds.has(node.id),
      selected: selectedId === node.id
    } satisfies StepNodeData
  })), [project, components, run, staleIds, selectedId]);
  const [nodes, setNodes, onNodesChange] = useNodesState(flowNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(project.edges.map(toReactEdge));
  useEffect(() => { setNodes(flowNodes); }, [flowNodes, setNodes]);
  useEffect(() => { setEdges(project.edges.map(toReactEdge)); }, [project.edges, setEdges]);
  useEffect(() => { api.components().then(setComponents).catch(() => setMessage("无法连接后端：组件目录加载失败，请确认服务已启动。")); }, []);

  const selectedNode = project.nodes.find((node) => node.id === selectedId);
  const selectedComponent = components.find((component) => component.type === selectedNode?.type);

  function markChanged(ids: string[]) {
    setRun((current) => {
      if (!current) return current;
      const affected = downstreamIds(project, ids);
      const results = { ...current.results };
      for (const id of affected) if (results[id]) results[id] = { ...results[id], status: "blocked", error: "配置已改变，旧结果失效", artifact: undefined };
      return { ...current, results };
    });
    setDirtyIds((current) => new Set([...current, ...ids]));
  }

  const updateProject = (producer: (draft: Project) => void, changed: string[] = []) => {
    setProject((current) => { const draft = structuredClone(current); producer(draft); return draft; });
    if (changed.length) markChanged(changed);
  };

  const addNode = (component: ComponentDef) => updateProject((draft) => draft.nodes.push(createNode(component, draft.nodes.length)));
  const patchNode = (id: string, patch: Partial<{ name: string }>) => updateProject((draft) => Object.assign(draft.nodes.find((node) => node.id === id)!, patch), [id]);
  const patchParam = (id: string, key: string, value: unknown) => updateProject((draft) => { draft.nodes.find((node) => node.id === id)!.params[key] = value; }, [id]);

  const connect = (connection: { source: string; target: string; sourceHandle?: string | null; targetHandle?: string | null }) => {
    const edge = { id: "e-" + crypto.randomUUID(), source: connection.source, target: connection.target, sourcePort: connection.sourceHandle ?? "rows", targetPort: connection.targetHandle ?? "rows" };
    setEdges((current) => addEdge(toReactEdge(edge), current));
    updateProject((draft) => draft.edges.push(edge), [edge.target]);
  };
  const persistPositions = (moved: Node[]) => {
    setProject((current) => {
      const draft = structuredClone(current);
      moved.forEach((movedNode) => {
        const target = draft.nodes.find((node) => node.id === movedNode.id);
        if (target) target.position = { x: movedNode.position.x, y: movedNode.position.y };
      });
      return draft;
    });
  };
  const deleteEdges = (deleted: Edge[]) => updateProject((draft) => {
    const ids = new Set(deleted.map((edge) => edge.id));
    draft.edges = draft.edges.filter((edge) => !ids.has(edge.id));
  }, deleted.map((edge) => edge.target));
  const deleteNodes = (deleted: Node[]) => {
    const ids = new Set(deleted.map((node) => node.id));
    setSelectedId((current) => current && ids.has(current) ? undefined : current);
    updateProject((draft) => {
      draft.nodes = draft.nodes.filter((node) => !ids.has(node.id));
      draft.edges = draft.edges.filter((edge) => !ids.has(edge.source) && !ids.has(edge.target));
    }, [...ids]);
  };

  function newBlank() {
    setProject(blankProject());
    setRun(undefined);
    setDeliveries({});
    setValidation(undefined);
    setDirtyIds(new Set());
    setSavedSnapshot(undefined);
    setSelectedId(undefined);
    setMessage("已新建空工程：从左侧添加组件并连线，保存后即可执行。");
  }

  function withdrawUnpublished() {
    if (!savedSnapshot) return;
    setProject(structuredClone(savedSnapshot.project));
    setRun(savedSnapshot.run ? structuredClone(savedSnapshot.run) : undefined);
    // 投递证据（deliveries）不属于未发布修订：已发出的结果保留，不随撤回删除。
    setDirtyIds(new Set());
    setValidation(undefined);
    setMessage("已撤回未发布的修订，重新打开 revision " + savedSnapshot.project.revision + " 的已保存版本。");
  }

  // 保存响应可能是 {project, validation}（成功）或 Validation（422 校验失败）。
  // 两种情况都立即反馈：成功可继续编辑，失败保留画布与本次输入并展示原因。
  function applySaveResponse(response: { project?: Project; validation?: Validation; issues?: Issue[]; ok?: boolean }): Project | undefined {
    if (response.project) {
      const saved = normalizeProject(response.project);
      setProject(saved);
      setValidation(response.validation);
      return saved;
    }
    if (response.issues || response.ok === false) {
      setValidation({ ok: false, issues: response.issues ?? [], executionOrder: [], affectedNodeIds: [] });
    }
    return undefined;
  }

  async function save() {
    let response: { project?: Project; validation?: Validation; issues?: Issue[]; ok?: boolean };
    try {
      response = await api.saveProject(project) as typeof response;
    } catch (error) {
      setMessage("保存失败：" + (error instanceof Error ? error.message : "无法连接后端") + "。画布与本次输入已保留，可直接重试。");
      return;
    }
    const saved = applySaveResponse(response);
    if (saved) {
      setSavedSnapshot({ project: structuredClone(saved), run });
      setDirtyIds(new Set());
      setMessage("已保存为 revision " + saved.revision + "，可立即继续编辑；未发布的修订可撤回。");
    } else {
      setMessage("校验未通过：未生成可执行版本，启动被禁止；画布与输入已保留，请按下方提示修正。");
    }
  }

  async function execute(startNodeIds: string[]) {
    let saveResponse: { project?: Project; validation?: Validation; issues?: Issue[]; ok?: boolean };
    try {
      saveResponse = await api.saveProject(project) as typeof saveResponse;
    } catch (error) {
      setMessage("保存失败：" + (error instanceof Error ? error.message : "无法连接后端") + "。运行器没有启动，页面保留当前状态。");
      return;
    }
    const saved = applySaveResponse(saveResponse);
    if (!saved) {
      setMessage("流程校验失败，运行器没有启动；画布与输入已保留。");
      return;
    }
    let result: RunResult;
    try {
      result = await api.run(saved.id, startNodeIds);
    } catch (error) {
      setMessage("运行请求失败：" + (error instanceof Error ? error.message : "后端不可用") + "，本轮没有产生新结果。");
      return;
    }
    setRun(result);
    if (result.deliveries) setDeliveries(result.deliveries);
    setDirtyIds(new Set());
    setSavedSnapshot({ project: structuredClone(saved), run: result });
    setMessage(result.ok ? "运行成功。" : "运行失败：下游已被阻断，不能把旧结果当成本轮输出。");
  }

  async function confirm(body: { nodeIds?: string[]; all?: boolean }) {
    setConfirming(true);
    try {
      // 确认绑定本页面实际看过的候选版本与运行批次：
      // 若另一窗口已重跑产生新候选，本次确认会被后端拒绝而不是误发布新结果。
      const expected = Object.values(deliveries)
        .filter((state) => state.pending && !state.pending.stale)
        .map((state) => ({ nodeId: state.nodeId, version: state.pending!.version, runId: state.pending!.runId }));
      const response = await api.confirm(project.id, { ...body, expected });
      setDeliveries(response.deliveries);
      setMessage(body.all ? "全部待确认结果已共同确认，对接收方可见。" : "该结果已先行确认，对接收方可见。");
    } catch (error) {
      setMessage("确认失败：" + (error instanceof Error ? error.message : "后端不可用") + "，待确认状态未变更。");
      // 拒绝证据已持久化：刷新投递状态，展示失效/拒绝记录与其它窗口产生的新候选。
      try {
        const loaded = await api.loadProject(project.id);
        if (loaded.deliveries) setDeliveries(loaded.deliveries);
      } catch { /* 保留本地状态 */ }
    } finally {
      setConfirming(false);
    }
  }

  async function reopen() {
    try {
      const loaded = await api.loadProject(project.id);
      const restored = normalizeProject(loaded.project);
      setProject(restored);
      setDeliveries(loaded.deliveries ?? {});
      const results: RunResult["results"] = {};
      Object.values(loaded.artifacts ?? {}).forEach((artifact) => { results[artifact.nodeId] = { nodeId: artifact.nodeId, status: "reused", artifact, reusedFrom: artifact.runId }; });
      setRun({ ok: true, runId: "saved-artifacts", projectVersion: "", affectedNodeIds: [], results });
      setDirtyIds(new Set());
      setSavedSnapshot({ project: structuredClone(restored), run: { ok: true, runId: "saved-artifacts", projectVersion: "", affectedNodeIds: [], results } });
      setValidation(undefined);
      const firstNode = restored.nodes[0];
      setSelectedId(firstNode?.id);
      setMessage("已重新打开保存的工程、版本化产物与各接收方投递状态。");
    } catch { setMessage("还没有可打开的已保存工程。"); }
  }

  const issues: Issue[] = validation?.issues ?? run?.issues ?? [];
  return <div className="app-shell">
    <header><h1>数据处理流程设计器</h1><div>{message}</div><div className="actions"><span className="revision">revision {project.revision}</span><button onClick={newBlank}>新建空工程</button><button onClick={reopen}>重新打开已保存版本</button>{isPublishedDirty && <button onClick={withdrawUnpublished}>撤回未发布修订</button>}<button onClick={save}>保存版本</button><button className="primary" onClick={() => execute([])}>整流程运行</button></div></header>
    <main>
      <nav className="palette"><h2>处理组件</h2>{components.map((component) => <button key={component.type} onClick={() => addNode(component)}><b>{component.name}</b><span>{component.category}</span></button>)}<p className="muted">新增组件复用 dataflow.project/v1 工程格式。</p></nav>
      <section className="canvas-wrap">
        <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={connect} onNodeDragStop={(_, moved) => persistPositions([moved])} onEdgesDelete={deleteEdges} onNodesDelete={deleteNodes} onNodeClick={(_, node) => setSelectedId(node.id)} fitView>
          <Background /><Controls />
        </ReactFlow>
        {issues.length > 0 && <div className="issues"><b>启动前校验</b>{issues.map((issue) => <div key={issue.code + issue.message}>• {issue.message}</div>)}</div>}
      </section>
      <Inspector project={project} node={selectedNode} component={selectedComponent} staleIds={staleIds} onPatchNode={patchNode} onPatchParam={patchParam} onRunFrom={() => selectedId && execute([selectedId])} />
    </main>
    <div className="bottom-panels">
      <DataPanel project={project} selectedId={selectedId} run={run} />
      <PublishPanel project={project} run={run} deliveries={deliveries} confirming={confirming} onConfirmAll={() => confirm({ all: true })} onConfirmOne={(nodeId) => confirm({ nodeIds: [nodeId] })} />
      <ComparePanel projectId={project.id} refreshKey={(run?.runId ?? "") + "|" + JSON.stringify(deliveries).length} />
    </div>
  </div>;
}
