import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  addEdge,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type Node
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import './styles.css'
import { api } from './api'
import { Inspector } from './Inspector'
import { WorkflowNode } from './WorkflowNode'
import type { WorkflowNodeData, WorkflowProject, WorkflowRun } from './types'
import { descendants, emptyProject, fromReactFlow, makeId, toReactFlow } from './workflowGraph'

const nodeTypes = { workflow: WorkflowNode }
const initial = emptyProject()

function detectCycle(edges: Edge[], candidate: Connection | Edge) {
  const adjacency = new Map<string, string[]>()
  ;[...edges, candidate as Edge].forEach((edge) => {
    adjacency.set(edge.source, [...(adjacency.get(edge.source) ?? []), edge.target])
  })
  const visiting = new Set<string>()
  const visited = new Set<string>()
  const walk = (id: string): boolean => {
    if (visiting.has(id)) return true
    if (visited.has(id)) return false
    visiting.add(id)
    for (const next of adjacency.get(id) ?? []) {
      if (walk(next)) return true
    }
    visiting.delete(id)
    visited.add(id)
    return false
  }
  return walk(candidate.source)
}

export default function App() {
  const graph = useMemo(() => toReactFlow(initial), [])
  const [projectName, setProjectName] = useState(initial.name)
  const [version, setVersion] = useState(0)
  const [nodes, setNodes, onNodesChange] = useNodesState<Node<WorkflowNodeData>>(graph.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>(graph.edges)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [run, setRun] = useState<WorkflowRun | null>(null)
  const [message, setMessage] = useState('')
  const [comment, setComment] = useState('')

  useEffect(() => {
    api.getProject(initial.id).then((saved) => {
      const loaded = toReactFlow(saved)
      setProjectName(saved.name)
      setVersion(saved.version)
      setNodes(loaded.nodes)
      setEdges(loaded.edges)
    }).catch(() => {
      setMessage('使用内置第一轮流程，保存后由 Go 持久化。')
    })
  }, [setEdges, setNodes])

  const selected = nodes.find((node) => node.id === selectedId) ?? null
  const rerunScope = selected ? descendants(edges, selected.id) : new Set<string>()
  const staleIds = useMemo(() => {
    if (!selected) return new Set<string>()
    return rerunScope
  }, [rerunScope, selected])

  const decoratedNodes = useMemo(() => nodes.map((node) => ({
    ...node,
    data: {
      ...node.data,
      status: run?.nodeStates[node.id]?.status,
      stale: staleIds.has(node.id),
      error: run?.nodeStates[node.id]?.error
    }
  })), [nodes, run, staleIds])

  const onConnect = useCallback((connection: Connection) => {
    if (!connection.sourceHandle || !connection.targetHandle) {
      setMessage('请使用明确的数据或参数手柄连接。')
      return
    }
    if (connection.source === connection.target || detectCycle(edges, connection)) {
      setMessage('已拦截循环连接：流程必须保持 DAG。')
      return
    }
    const kind = connection.sourceHandle.includes('parameter') && connection.targetHandle.includes('parameter') ? 'parameter' : 'data'
    setEdges((current) => addEdge({
      ...connection,
      id: makeId(kind),
      label: kind === 'parameter' ? '参数:suffix' : '数据',
      data: { kind, parameter: kind === 'parameter' ? 'suffix' : undefined },
      animated: kind === 'data',
      style: kind === 'parameter' ? { stroke: '#7c3aed', strokeDasharray: '6 3' } : { stroke: '#0891b2' }
    }, current))
    setMessage(kind === 'parameter' ? '参数依赖不会传输行数据。' : '数据依赖会传递上游实际结果。')
  }, [edges, setEdges])

  const updateNode = (updated: Node<WorkflowNodeData>) => {
    setNodes((current) => current.map((node) => node.id === updated.id ? updated : node))
    setRun((current) => current ? {
      ...current,
      nodeStates: Object.fromEntries(Object.entries(current.nodeStates).map(([id, state]) => rerunScope.has(id)
        ? [id, { ...state, status: 'stale', data: [], preview: [], error: '编辑后失效，不能复用旧结果' }]
        : [id, state]))
    } : current)
  }

  const addNode = (type: WorkflowNodeData['type']) => {
    const labels = { 'file-input': '读取文件', transform: '转换', manual: '人工分支', 'file-output': '写出文件' }
    const id = makeId(type)
    setNodes((current) => [...current, {
      id,
      type: 'workflow',
      position: { x: 120 + current.length * 30, y: 80 + current.length * 35 },
      data: { label: labels[type], type, params: type === 'transform' ? { operation: 'uppercase', suffix: '' } : { content: 'new\nrows' } }
    }])
    setSelectedId(id)
  }

  const projectForSave = (): WorkflowProject => fromReactFlow(projectName, version, nodes, edges)

  const save = async () => {
    try {
      const saved = await api.saveProject(projectForSave())
      setVersion(saved.project.version)
      setMessage('已保存版本 v' + saved.project.version + '，重新打开仍可执行。')
    } catch (err) {
      setMessage(String(err))
    }
  }

  const startRun = async (fromNodeId?: string) => {
    try {
      const saved = await api.saveProject(projectForSave())
      setVersion(saved.project.version)
      const result = await api.startRun(initial.id, fromNodeId)
      setRun(result)
      setMessage(fromNodeId ? '局部重跑范围：' + Array.from(descendants(edges, fromNodeId)).join('、') : '全流程运行完成。')
    } catch (err) {
      setMessage(String(err))
    }
  }

  const decide = async (approved: boolean) => {
    if (!run) return
    try {
      setRun(await api.decide(run.id, approved, comment))
      setMessage(approved ? '人工分支已放行，汇合点使用本轮数据。' : '人工分支已拒绝，下游标记失败。')
    } catch (err) {
      setMessage(String(err))
    }
  }

  const waiting = run ? Object.values(run.nodeStates).find((state) => state.status === 'waiting') : undefined

  return (
    <div className="app-shell">
      <header>
        <div>
          <h1>数据处理流程设计器</h1>
          <p>React 负责连步骤，Go 负责执行、版本与结果状态。当前版本 v{version}</p>
        </div>
        <input value={projectName} onChange={(e) => setProjectName(e.target.value)} />
        <button onClick={save}>保存流程</button>
        <button className="primary" onClick={() => startRun()}>启动全流程</button>
        <button disabled={!selected} onClick={() => selected && startRun(selected.id)}>从选中步骤局部重跑</button>
      </header>
      {message && <div className="message">{message}</div>}
      <main>
        <div className="canvas">
          <ReactFlow
            nodes={decoratedNodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={(_, node) => setSelectedId(node.id)}
            fitView
          >
            <Background />
            <Controls />
            <MiniMap />
          </ReactFlow>
          {run && <div className={'run-status ' + run.status}>运行：{run.status} · {run.id} · 工程 v{run.projectVersion}</div>}
          {waiting && <div className="manual-panel">
            <strong>人工分支等待处理：{waiting.id}</strong>
            <input placeholder="审批备注" value={comment} onChange={(e) => setComment(e.target.value)} />
            <button onClick={() => decide(true)}>批准分支</button>
            <button onClick={() => decide(false)}>拒绝分支</button>
          </div>}
        </div>
        <Inspector node={selected} nodes={nodes} edges={edges} run={run} onUpdate={updateNode} onAdd={addNode} />
      </main>
    </div>
  )
}
