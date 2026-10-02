import { useEffect, useMemo, useState } from "react";
import { Background, Controls, ReactFlow, addEdge, useEdgesState, useNodesState, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "./api";
import { blankProject, createNode, downstreamIds, emptyProject, normalizeProject, toReactEdge } from "./project";
import { DataPanel } from "./components/DataPanel";
import { Inspector } from "./components/Inspector";
import { StepNode, type StepNodeData } from "./components/StepNode";
import type { ComponentDef, Issue, Project, Publication, PublishPolicy, RunResult, Validation } from "./types";

const nodeTypes = { step: StepNode };

// 后端对没有端口的组件会返回 []（旧数据里也可能出现 null/undefined），
// 统一归一化为数组，避免空端口列表被当成普通列表 map 时整页崩溃。
function withEmptyPorts(component: ComponentDef | undefined): ComponentDef | undefined {
  if (!component) return component;
  return { ...component, inputs: component.inputs ?? [], outputs: component.outputs ?? [], params: component.params ?? [] };
}

export default function App() {
  const [components, setComponents] = useState<ComponentDef[]>([]);
  const [project, setProjectState] = useState<Project>(() => normalizeProject(emptyProject()));
  const [selectedId, setSelectedId] = useState<string | undefined>("filter-1");
  const [validation, setValidation] = useState<Validation>();
  const [run, setRun] = useState<RunResult>();
  const [publications, setPublications] = useState<Record<string, Publication>>({});
  const [publishPolicy, setPublishPolicy] = useState<PublishPolicy>("all");
  const [saveState, setSaveState] = useState<"idle" | "saving" | "failed">("idle");
  const [lastFailure, setLastFailure] = useState<string>("");
  const [dirtyIds, setDirtyIds] = useState<Set<string>>(new Set());
  const staleIds = useMemo(() => downstreamIds(project, [...dirtyIds]), [project, dirtyIds]);
  const [savedSnapshot, setSavedSnapshot] = useState<{ project: Project; run?: RunResult }>();
  const [message, setMessage] = useState("先保存，再执行；空工程与空端口节点也能直接进入编辑。");
  const isPublishedDirty = useMemo(() => {
    if (!savedSnapshot) return false;
    return JSON.stringify({ nodes: project.nodes, edges: project.edges }) !== JSON.stringify({ nodes: savedSnapshot.project.nodes, edges: savedSnapshot.project.edges });
  }, [project, savedSnapshot]);
  const setProject = (next: Project | ((current: Project) => Project)) => {
    setProjectState((current) => normalizeProject(typeof next === "function" ? (next as (current: Project) => Project)(current) : next));
  };
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
    setProject(normalizeProject(blankProject()));
    setRun(undefined);
    setValidation(undefined);
    setDirtyIds(new Set());
    setSavedSnapshot(undefined);
    setPublications({});
    setSaveState("idle");
    setSelectedId(undefined);
    setMessage("已新建空工程：从左侧添加组件并连线，保存后即可执行。");
  }

  function withdrawUnpublished() {
    if (!savedSnapshot) return;
    // 撤回只回退未发布修订：已经发出的发布凭证不会被删除。
    const snapshotProject = normalizeProject(structuredClone(savedSnapshot.project));
    setProject(snapshotProject);
    setRun(savedSnapshot.run ? structuredClone(savedSnapshot.run) : undefined);
    setPublications((current) => structuredClone(current));
    setDirtyIds(new Set());
    setValidation(undefined);
    setMessage("已撤回未发布的修订，重新打开 revision " + savedSnapshot.project.revision + " 的已保存版本。");
  }

  type SaveResponse = { project?: Project; validation: Validation; publications?: Record<string, Publication> };

  async function saveProjectOnce(canvasBefore: Project): Promise<SaveResponse> {
    setSaveState("saving");
    setLastFailure("");
    try {
      const response = await api.saveProject(canvasBefore) as SaveResponse | Validation;
      setSaveState("idle");
      return response as SaveResponse;
    } catch (error) {
      // 真实失败：不触碰画布与本次输入，进入可恢复状态，可立即重试。
      setProject(normalizeProject(canvasBefore));
      setSaveState("failed");
      const detail = error instanceof Error ? error.message : "未知错误";
      setLastFailure(detail);
      throw error;
    }
  }

  function applyPublications(next?: Record<string, Publication>) {
    if (next) setPublications(next);
  }

  async function save() {
    // 保存前画布与输入原样保留（函数式读取最新状态也不修改它）。
    const canvasBefore = normalizeProject(project);
    let response: SaveResponse;
    try {
      response = await saveProjectOnce(canvasBefore);
    } catch (error) {
      setMessage("保存失败：画布和本次输入均已保留，可直接重试或继续编辑。" + (error instanceof Error ? "（" + error.message + "）" : ""));
      return;
    }
    setValidation(response.validation);
    if (response.project) {
      const saved = normalizeProject(response.project);
      setProject(saved);
      applyPublications(response.publications);
      setSavedSnapshot({ project: structuredClone(saved), run });
      setDirtyIds(new Set());
      setMessage("已保存为 revision " + saved.revision + "，可以立即继续编辑；未发布的修订可撤回。");
    } else {
      setMessage("校验未通过：未生成可执行版本，画布与输入保留，可修改后重试。");
    }
  }

  async function execute(startNodeIds: string[]) {
    const canvasBefore = normalizeProject(project);
    let saveResponse: SaveResponse;
    try {
      saveResponse = await saveProjectOnce(canvasBefore);
    } catch (error) {
      setMessage("运行前保存失败：运行器没有启动，画布保留，可重试运行。" + (error instanceof Error ? "（" + error.message + "）" : ""));
      return;
    }
    setValidation(saveResponse.validation);
    if (!saveResponse.project) {
      setMessage("流程校验失败，运行器没有启动；画布保留可继续修改。");
      return;
    }
    const saved = normalizeProject(saveResponse.project);
    setProject(saved);
    applyPublications(saveResponse.publications);
    let result: RunResult;
    try {
      result = await api.run(saved.id, startNodeIds, publishPolicy);
    } catch (error) {
      setSaveState("failed");
      setLastFailure(error instanceof Error ? error.message : "运行请求失败");
      setMessage("运行请求真实失败：本轮没有产生结果，画布保留，可直接重试。" + (error instanceof Error ? "（" + error.message + "）" : ""));
      return;
    }
    setRun(result);
    if (result.publications) setPublications(result.publications);
    setDirtyIds(new Set());
    setSavedSnapshot({ project: structuredClone(saved), run: result });
    setSaveState("idle");
    const publishedCount = Object.values(result.results).filter((item) => item.visibility === "published").length;
    const computedCount = Object.values(result.results).filter((item) => item.visibility === "computed").length;
    if (result.ok) {
      setMessage("运行完成：" + publishedCount + " 个结果已对接收方可见。");
    } else {
      setMessage("运行出现失败：" + publishedCount + " 个已发布、" + computedCount + " 个仅已计算；已发布的旧版本对接收方仍可见。");
    }
  }

  async function reopen() {
    try {
      const loaded = await api.loadProject(project.id);
      setProject(normalizeProject(loaded.project));
      const results: RunResult["results"] = {};
      Object.values(loaded.artifacts ?? {}).forEach((artifact) => { results[artifact.nodeId] = { nodeId: artifact.nodeId, status: "reused", artifact, reusedFrom: artifact.runId }; });
      const loadedPublications = loaded.publications ?? {};
      Object.values(loadedPublications).forEach((publication) => {
        if (results[publication.nodeId]) {
          results[publication.nodeId] = { ...results[publication.nodeId], visibility: "already", recipient: publication.recipient, published: publication };
        }
      });
      setPublications(loadedPublications);
      setRun({ ok: true, runId: "saved-artifacts", projectVersion: "", affectedNodeIds: [], results, publications: loadedPublications });
      setDirtyIds(new Set());
      setSavedSnapshot({ project: structuredClone(normalizeProject(loaded.project)), run: { ok: true, runId: "saved-artifacts", projectVersion: "", affectedNodeIds: [], results, publications: loadedPublications } });
      setValidation(undefined);
      const firstNode = normalizeProject(loaded.project).nodes[0];
      setSelectedId(firstNode?.id);
      setMessage("已重新打开保存的工程：可核对每个接收方当前可见的已发布版本。");
    } catch (error) {
      setSaveState("failed");
      setLastFailure(error instanceof Error ? error.message : "重新打开失败");
      setMessage("重新打开失败：当前画布未受影响。" + (error instanceof Error ? "（" + error.message + "）" : ""));
    }
  }

  const issues: Issue[] = validation?.issues ?? run?.issues ?? [];
  const recipientOutputs = project.nodes.filter((node) => node.type === "file_output");
  return <div className="app-shell">
    <header><h1>数据处理流程设计器</h1><div>{message}</div>
      {saveState === "failed" && <div className="banner danger save-banner"><b>保存/运行请求失败（可恢复）</b><span>画布与本次输入均已保留，未替换为错误结果。原因：{lastFailure}</span><div><button onClick={save}>重试保存</button><button className="primary" onClick={() => execute([])}>重试整流程运行</button><button onClick={() => setSaveState("idle")}>继续编辑</button></div></div>}
      <div className="actions"><span className="revision">revision {project.revision}</span><button onClick={newBlank}>新建空工程</button><button onClick={reopen}>重新打开已保存版本</button>{isPublishedDirty && <button onClick={withdrawUnpublished}>撤回未发布修订</button>}<button onClick={save} disabled={saveState === "saving"}>{saveState === "saving" ? "保存中…" : "保存版本"}</button>
        <label className="policy">多接收方发布
          <select value={publishPolicy} onChange={(event) => setPublishPolicy(event.target.value as PublishPolicy)}>
            <option value="all">全部结果共同确认</option>
            <option value="partial">允许部分结果先行</option>
          </select>
        </label>
        <button className="primary" onClick={() => execute([])} disabled={saveState === "saving"}>整流程运行（{recipientOutputs.length} 个接收方）</button>
      </div>
    </header>
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
    <DataPanel project={project} selectedId={selectedId} run={run} onSelectNode={setSelectedId} />
  </div>;
}
