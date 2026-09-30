import { Component, useEffect, useMemo, useRef, useState, type ErrorInfo, type ReactNode } from "react";
import { Background, Controls, ReactFlow, addEdge, useEdgesState, useNodesState, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { api } from "./api";
import { blankProject, createNode, downstreamIds, sampleProject, toReactEdge } from "./project";
import { DataPanel } from "./components/DataPanel";
import { Inspector } from "./components/Inspector";
import { StepNode, type StepNodeData } from "./components/StepNode";
import type { ComponentDef, Issue, NodeRunResult, Project, RunResult, Validation } from "./types";

const nodeTypes = { step: StepNode };

class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("页面渲染失败：", error, info.componentStack);
  }
  render() {
    if (this.state.error) {
      return (
        <div style={{ padding: 24, fontFamily: "sans-serif" }}>
          <h2>页面渲染失败</h2>
          <pre style={{ whiteSpace: "pre-wrap", color: "#991b1b" }}>{String(this.state.error.stack ?? this.state.error)}</pre>
          <button onClick={() => location.reload()}>重新加载页面</button>
        </div>
      );
    }
    return this.props.children;
  }
}

function isValidation(value: unknown): value is Validation {
  return !!value && typeof value === "object" && Array.isArray((value as Validation).issues);
}

export default function App() {
  return <ErrorBoundary><Designer /></ErrorBoundary>;
}

function Designer() {
  const [components, setComponents] = useState<ComponentDef[]>([]);
  const [project, setProject] = useState<Project>(() => sampleProject());
  const [selectedId, setSelectedId] = useState<string | undefined>("filter-1");
  const [validation, setValidation] = useState<Validation>();
  const [run, setRun] = useState<RunResult>();
  const [dirtyIds, setDirtyIds] = useState<Set<string>>(new Set());
  const [projectVersion, setProjectVersion] = useState("");
  const projectRef = useRef(project);
  projectRef.current = project;
  const staleIds = useMemo(() => downstreamIds(project, [...dirtyIds]), [project, dirtyIds]);
  const [message, setMessage] = useState("先保存，再执行；循环、缺参数和无效数据端口会在启动前拦截。");

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
    api.components().then(setComponents).catch((error: Error) => setMessage(error.message + "（组件面板暂时不可用，但工程仍可编辑）"));
  }, []);

  const selectedNode = project.nodes.find((node) => node.id === selectedId);
  const selectedComponent = components.find((component) => component.type === selectedNode?.type);

  function markChangedOn(base: Project, current: RunResult | undefined, ids: string[], nextEdges: Project["edges"] = base.edges): RunResult | undefined {
    if (!current) return current;
    const affected = downstreamIds(base, ids, nextEdges);
    const results = { ...current.results };
    for (const id of affected) {
      const previous = results[id];
      if (!previous) continue;
      const next: NodeRunResult = { ...previous, status: "blocked", error: "配置或数据依赖已改变，旧产物在本轮重算前不可用", artifact: undefined, reusedFrom: undefined, basis: "自上一轮运行后发生修改" };
      results[id] = next;
    }
    return { ...current, results, ok: Object.values(results).every((item) => item.status !== "failed" && item.status !== "blocked") };
  }

  const updateProject = (producer: (draft: Project) => void, changed: string[] = []) => {
    let nextProject: Project = project;
    setProject((current) => {
      const draft = structuredClone(current);
      producer(draft);
      nextProject = draft;
      return draft;
    });
    if (changed.length) {
      setRun((current) => markChangedOn(nextProject, current, changed));
      setDirtyIds((current) => new Set([...current, ...changed]));
    }
  };

  const addNode = (component: ComponentDef) => updateProject((draft) => draft.nodes.push(createNode(component, draft.nodes.length)));
  const patchNode = (id: string, patch: Partial<{ name: string }>) => updateProject((draft) => Object.assign(draft.nodes.find((node) => node.id === id)!, patch), [id]);
  const patchParam = (id: string, key: string, value: unknown) => updateProject((draft) => { draft.nodes.find((node) => node.id === id)!.params[key] = value; }, [id]);

  const newBlankProject = () => {
    setProject(blankProject());
    setRun(undefined);
    setValidation(undefined);
    setDirtyIds(new Set());
    setSelectedId(undefined);
    setProjectVersion("");
    setMessage("已新建空白工程：从左侧组件面板拖入或点击组件即可开始，数据连线与参数分别配置。");
  };
  const loadSample = () => {
    const sample = sampleProject();
    setProject(sample);
    setRun(undefined);
    setValidation(undefined);
    setDirtyIds(new Set());
    setSelectedId("filter-1");
    setProjectVersion("");
    setMessage("已载入自带样例：包含共享前置、分支汇合，可直接保存并执行。");
  };

  const connect = (connection: { source: string; target: string; sourceHandle?: string | null; targetHandle?: string | null }) => {
    const edge = { id: "e-" + crypto.randomUUID(), source: connection.source, target: connection.target, sourcePort: connection.sourceHandle ?? "rows", targetPort: connection.targetHandle ?? "rows" };
    const base = projectRef.current;
    const nextEdges = [...base.edges, edge];
    setProject((current) => ({ ...current, edges: [...current.edges, edge] }));
    setEdges((current) => addEdge(toReactEdge(edge), current));
    setRun((current) => markChangedOn(base, current, [edge.target], nextEdges));
    setDirtyIds((current) => new Set([...current, ...downstreamIds(base, [edge.target], nextEdges)]));
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

  async function save(silent = false): Promise<boolean> {
    try {
      const response = await api.saveProject(project);
      if (isValidation(response) && !response.ok) {
        setValidation(response);
        setMessage("校验未通过：未生成可执行版本，运行被禁止。");
        return false;
      }
      const saved = response as { project?: Project; validation: Validation };
      setValidation(saved.validation);
      if (saved.project) {
        setProject(saved.project);
        if (!silent) setMessage("已保存为 revision " + saved.project.revision + "，工程可关闭后重新打开执行。");
        return true;
      }
      return false;
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "保存失败");
      return false;
    }
  }

  async function execute(startNodeIds: string[]) {
    const savedOK = await save(true);
    if (!savedOK) return;
    const savedProject = projectRef.current;
    try {
      const result = await api.run(savedProject.id, startNodeIds);
      setRun(result);
      setDirtyIds(new Set());
      if (result.ok) {
        const reused = Object.values(result.results).filter((item) => item.status === "reused").length;
        const recomputed = result.affectedNodeIds.length;
        setMessage((startNodeIds.length ? "局部重跑" : "整流程运行") + "成功：本轮重算 " + recomputed + " 步，沿用上一轮产物 " + reused + " 步；节点与数据面板给出了每一步的依据。");
      } else {
        const failed = Object.values(result.results).filter((item) => item.status === "failed");
        setMessage("运行失败：" + failed.map((item) => item.nodeId + "（" + item.error + "）").join("；") + "。依赖步骤已阻断，不会把上一轮产物显示为本轮成功。");
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "运行失败");
    }
  }

  async function reopen() {
    try {
      const loaded = await api.loadProject(projectRef.current.id);
      setProject(loaded.project);
      setProjectVersion(loaded.projectVersion);
      setDirtyIds(new Set());
      if (loaded.hasRun && loaded.latestRun) {
        const latestVersion = loaded.latestRun.projectVersion;
        const versionMatches = latestVersion === loaded.projectVersion;
        if (versionMatches) {
          setRun(loaded.latestRun);
          const reused = Object.values(loaded.latestRun.results).filter((item) => item.status === "reused").length;
          setMessage("已重新打开 revision " + loaded.project.revision + "：页面恢复 " + loaded.latestRun.runId + " 的真实运行结果（复用 " + reused + " 步，重算 " + loaded.latestRun.affectedNodeIds.length + " 步）。");
        } else {
          const results: RunResult["results"] = {};
          Object.values(loaded.artifacts ?? {}).forEach((artifact) => {
            results[artifact.nodeId] = { nodeId: artifact.nodeId, status: "blocked", artifact: undefined, error: "工程已在运行后被修改（页面版本与产物版本不一致），旧产物不能当作本轮成功，请重新执行", basis: "重开时版本核对：产物来自 " + artifact.runId };
          });
          setRun({ ok: false, runId: loaded.latestRun.runId, projectVersion: latestVersion, affectedNodeIds: [], results, issues: loaded.latestRun.issues });
          setMessage("已重新打开 revision " + loaded.project.revision + "：最后一次运行基于旧版本，旧产物已标记失效，需重新运行。");
        }
      } else {
        const results: RunResult["results"] = {};
        Object.values(loaded.artifacts ?? {}).forEach((artifact) => {
          const stale = artifact.projectVersion !== undefined && artifact.projectVersion !== loaded.projectVersion;
          results[artifact.nodeId] = stale
            ? { nodeId: artifact.nodeId, status: "blocked", error: "产物来自旧工程版本，请重新执行", basis: "重开版本核对不一致", artifact: undefined }
            : { nodeId: artifact.nodeId, status: "reused", artifact, reusedFrom: artifact.runId, basis: "重新打开：沿用已保存产物 " + artifact.runId + "，节点版本 " + artifact.version };
        });
        setRun({ ok: Object.values(results).every((item) => item.status !== "blocked"), runId: "saved-artifacts", projectVersion: loaded.projectVersion, affectedNodeIds: [], results });
        setMessage("已重新打开保存的工程" + (loaded.artifacts && Object.keys(loaded.artifacts).length ? "和版本化产物。" : "，还没有运行产物。"));
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "还没有可打开的已保存工程。");
    }
  }

  const issues: Issue[] = validation?.issues ?? [];
  const runIssues: Issue[] = run?.issues ?? [];
  const allIssues = issues.length ? issues : runIssues;

  return (
    <div className="app-shell">
      <header>
        <h1>数据处理流程设计器</h1>
        <div>{message}</div>
        <div className="actions">
          <button onClick={newBlankProject}>空白工程</button>
          <button onClick={loadSample}>载入样例</button>
          <button onClick={reopen}>重新打开</button>
          <button onClick={() => void save()}>保存版本</button>
          <button className="primary" onClick={() => void execute([])}>整流程运行</button>
        </div>
      </header>
      <main>
        <nav className="palette">
          <h2>处理组件</h2>
          {components.map((component) => <button key={component.type} onClick={() => addNode(component)}><b>{component.name}</b><span>{component.category}</span></button>)}
          <p className="muted">点击组件添加步骤；参数在右侧编辑，数据依赖通过端口连线建立，两者在运行前分别校验。</p>
        </nav>
        <section className="canvas-wrap">
          <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={connect} onNodeDragStop={(_, moved) => persistPositions([moved])} onEdgesDelete={deleteEdges} onNodesDelete={deleteNodes} onNodeClick={(_, node) => setSelectedId(node.id)} fitView>
            <Background /><Controls />
          </ReactFlow>
          {allIssues.length > 0 && <div className="issues"><b>运行前校验未通过，执行被阻断</b>{allIssues.map((issue) => <div key={issue.code + issue.message}>• {issue.message}</div>)}</div>}
        </section>
        <Inspector project={project} node={selectedNode} component={selectedComponent} staleIds={staleIds} onPatchNode={patchNode} onPatchParam={patchParam} onRunFrom={() => selectedId && void execute([selectedId])} />
      </main>
      <DataPanel project={project} selectedId={selectedId} run={run} projectVersion={projectVersion} />
    </div>
  );
}
