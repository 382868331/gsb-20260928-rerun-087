import { useEffect, useMemo, useState } from "react";
import { Background, Controls, ReactFlow, addEdge, useEdgesState, useNodesState, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "./api";
import { createNode, downstreamIds, emptyProject, toReactEdge } from "./project";
import { DataPanel } from "./components/DataPanel";
import { Inspector } from "./components/Inspector";
import { StepNode, type StepNodeData } from "./components/StepNode";
import type { ComponentDef, Issue, Project, RunResult, Validation } from "./types";

const nodeTypes = { step: StepNode };

export default function App() {
  const [components, setComponents] = useState<ComponentDef[]>([]);
  const [project, setProject] = useState<Project>(emptyProject);
  const [selectedId, setSelectedId] = useState<string | undefined>("filter-1");
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
  useEffect(() => { api.components().then(setComponents); }, []);

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

  async function save() {
    const response = await api.saveProject(project) as { project?: Project; validation: Validation };
    setValidation(response.validation);
    if (response.project) {
      setProject(response.project);
      setMessage("已保存为 revision " + response.project.revision + "，工程可关闭后重新打开执行。");
    } else {
      setMessage("校验未通过：未生成可执行版本，启动被禁止。");
    }
  }

  async function execute(startNodeIds: string[]) {
    const saveResponse = await api.saveProject(project) as { project?: Project; validation: Validation };
    setValidation(saveResponse.validation);
    if (!saveResponse.project) {
      setMessage("流程校验失败，运行器没有启动。");
      return;
    }
    setProject(saveResponse.project);
    const result = await api.run(saveResponse.project.id, startNodeIds);
    setRun(result);
    setDirtyIds(new Set());
    setMessage(result.ok ? "运行成功。" : "运行失败：下游已被阻断，不能把旧结果当成本轮输出。");
  }

  async function reopen() {
    try {
      const loaded = await api.loadProject(project.id);
      setProject(loaded.project);
      const results: RunResult["results"] = {};
      Object.values(loaded.artifacts ?? {}).forEach((artifact) => { results[artifact.nodeId] = { nodeId: artifact.nodeId, status: "reused", artifact, reusedFrom: artifact.runId }; });
      setRun({ ok: true, runId: "saved-artifacts", projectVersion: "", affectedNodeIds: [], results });
      setDirtyIds(new Set());
      setMessage("已重新打开保存的工程和版本化产物。");
    } catch { setMessage("还没有可打开的已保存工程。"); }
  }

  const issues: Issue[] = validation?.issues ?? run?.issues ?? [];
  return <div className="app-shell">
    <header><h1>数据处理流程设计器</h1><div>{message}</div><div className="actions"><button onClick={reopen}>重新打开</button><button onClick={save}>保存版本</button><button className="primary" onClick={() => execute([])}>整流程运行</button></div></header>
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
