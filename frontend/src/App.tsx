import { useEffect, useMemo, useState } from "react";
import { Background, Controls, ReactFlow, addEdge, useEdgesState, useNodesState, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "./api";
import { blankProject, createNode, downstreamIds, emptyProject, toReactEdge } from "./project";
import { DataPanel } from "./components/DataPanel";
import { Inspector } from "./components/Inspector";
import { StepNode, type StepNodeData } from "./components/StepNode";
import type { Artifact, ComponentDef, Issue, Project, RunResult, Validation } from "./types";

const nodeTypes = { step: StepNode };

function artifactsFromResult(result: RunResult | undefined, previous?: RunResult): Record<string, Artifact> {
  const artifacts: Record<string, Artifact> = {};
  // Start from the previous saved artifacts so a run that only covers part of
  // the graph still keeps the reused branch artifacts on disk for reopen.
  Object.values(previous?.results ?? {}).forEach((entry) => {
    if (entry.artifact && (entry.status === "reused" || entry.status === "succeeded")) artifacts[entry.artifact.nodeId] = entry.artifact;
  });
  Object.values(result?.results ?? {}).forEach((entry) => {
    if (entry.artifact && entry.status === "succeeded") artifacts[entry.artifact.nodeId] = entry.artifact;
    if ((entry.status === "failed" || entry.status === "blocked") && entry.nodeId) delete artifacts[entry.nodeId];
  });
  return artifacts;
}

function artifactsFromRun(result: RunResult | undefined): Record<string, Artifact> {
  return artifactsFromResult(undefined, result);
}

export default function App() {
  const [components, setComponents] = useState<ComponentDef[]>([]);
  const [project, setProject] = useState<Project>(emptyProject);
  const [selectedId, setSelectedId] = useState<string | undefined>("filter-1");
  const [savedSnapshot, setSavedSnapshot] = useState<{ project: Project; artifacts: Record<string, Artifact> }>();
  const [validation, setValidation] = useState<Validation>();
  const [run, setRun] = useState<RunResult>();
  const [dirtyIds, setDirtyIds] = useState<Set<string>>(new Set());
  const staleIds = useMemo(() => downstreamIds(project, [...dirtyIds]), [project, dirtyIds]);
  const [message, setMessage] = useState("先保存，再执行；循环会在启动前拦截。");
  const flowNodes = useMemo(() => project.nodes.map((node) => ({
    id: node.id,
    type: "step",
    position: node.position,
    data: {
      node,
      component: components.find((item) => item.type === node.type),
      result: run?.results[node.id],
      stale: staleIds.has(node.id),
      selected: selectedId === node.id
    } satisfies StepNodeData
  })), [project, components, run, staleIds, selectedId]);
  const [nodes, setNodes, onNodesChange] = useNodesState(flowNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(project.edges.map(toReactEdge));
  useEffect(() => { setNodes(flowNodes); }, [flowNodes, setNodes]);
  useEffect(() => { setEdges(project.edges.map(toReactEdge)); }, [project.edges, setEdges]);
  useEffect(() => {
    api.components()
      .then((items) => setComponents(Array.isArray(items) ? items : []))
      .catch(() => setMessage("无法连接后端：组件目录加载失败，当前画布仍可编辑。"));
  }, []);

  const selectedNode = project.nodes.find((node) => node.id === selectedId);
  const selectedComponent = components.find((component) => component.type === selectedNode?.type);

  const hasUnpublishedRevision = useMemo(() => {
    if (!savedSnapshot) return false;
    return JSON.stringify({ ...project, revision: 0 }) !== JSON.stringify({ ...savedSnapshot.project, revision: 0 });
  }, [project, savedSnapshot]);

  function applySaved(projectValue: Project, artifactsValue: Record<string, Artifact> = {}, sourceMessage?: string) {
    setSavedSnapshot({ project: structuredClone(projectValue), artifacts: structuredClone(artifactsValue) });
    setProject(projectValue);
    const results: RunResult["results"] = {};
    Object.values(artifactsValue ?? {}).forEach((artifact) => {
      results[artifact.nodeId] = { nodeId: artifact.nodeId, status: "reused", artifact, reusedFrom: artifact.runId, changeBasis: "unchanged" };
    });
    setRun({ ok: true, runId: "saved-artifacts", projectVersion: "", affectedNodeIds: [], results });
    setDirtyIds(new Set());
    if (sourceMessage !== undefined) setMessage(sourceMessage);
  }

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
    setValidation(undefined);
    setDirtyIds(new Set());
    setSelectedId(undefined);
    setMessage("已新建空工程：从左侧添加组件并连线，保存后即可执行。");
  }

  async function save() {
    let response: { project?: Project; validation: Validation };
    try {
      response = await api.saveProject(project) as { project?: Project; validation: Validation };
    } catch {
      setMessage("无法连接后端：保存失败，页面状态未变更。");
      return;
    }
    setValidation(response.validation);
    if (response.project) {
      applySaved(response.project, artifactsFromRun(run), "已保存为 revision " + response.project.revision + "，未发布的后续修改可随时撤回。");
    } else {
      setMessage("校验未通过：未生成可执行版本，启动被禁止。");
    }
}

  async function execute(startNodeIds: string[]) {
    let saveResponse: { project?: Project; validation: Validation };
    try {
      saveResponse = await api.saveProject(project) as { project?: Project; validation: Validation };
    } catch {
      setMessage("无法连接后端：运行器没有启动，页面保留当前状态。");
      return;
    }
    setValidation(saveResponse.validation);
    if (!saveResponse.project) {
      setMessage("流程校验失败，运行器没有启动。");
      return;
    }
    let result: RunResult;
    try {
      result = await api.run(saveResponse.project.id, startNodeIds);
    } catch {
      setMessage("运行请求失败：后端不可用，本轮没有产生新结果。");
      applySaved(saveResponse.project, artifactsFromRun(run), "保存成功但运行接口暂时失败：画布与已有产物已保留，稍后可重试执行。");
      return;
    }
    const artifacts = artifactsFromResult(result, run);
    setSavedSnapshot({ project: saveResponse.project, artifacts });
    setProject(saveResponse.project);
    setRun(result);
    setDirtyIds(new Set());
    setMessage(result.ok ? "运行成功。" : "运行完成但存在失败分支：下游已阻断，旧成功数据不会冒充本轮结果。");
  }

  async function reopen() {
    try {
      const loaded = await api.loadProject(project.id);
      applySaved(loaded.project, loaded.artifacts ?? {}, "已重新打开保存的 revision " + loaded.project.revision + " 和版本化产物。");
      setSelectedId(undefined);
    } catch { setMessage("还没有可打开的已保存工程。"); }
  }

  function discardRevision() {
    if (!savedSnapshot) {
      setMessage("还没有已保存版本可回退。");
      return;
    }
    applySaved(savedSnapshot.project, savedSnapshot.artifacts, "已撤回未发布的修订，回到保存的 revision " + savedSnapshot.project.revision + "。");
    setValidation(undefined);
  }

  const issues: Issue[] = validation?.issues ?? run?.issues ?? [];
  return <div className="app-shell">
    <header><h1>数据处理流程设计器</h1><div>{message}</div><div className="actions"><span className="revision">revision {project.revision}{hasUnpublishedRevision ? " · 有未发布修订" : ""}</span><button onClick={newBlank}>新建空工程</button><button onClick={discardRevision} disabled={!hasUnpublishedRevision}>撤回未发布修订</button><button onClick={reopen}>重新打开已保存版本</button><button onClick={save}>保存版本</button><button className="primary" onClick={() => execute([])}>整流程运行</button></div></header>
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
    <DataPanel project={project} selectedId={selectedId} run={run} />
  </div>;
}
