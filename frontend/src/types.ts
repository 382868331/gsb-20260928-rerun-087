export type NodeType = 'file-input' | 'transform' | 'file-output' | 'manual'

export interface WorkflowNodeData {
  [key: string]: unknown
  label: string
  type: NodeType
  params: Record<string, string>
  status?: string
  stale?: boolean
  error?: string
}

export interface WorkflowProject {
  schemaVersion?: string
  id: string
  name: string
  version: number
  nodes: Array<{
    id: string
    type: NodeType
    name: string
    position: { x: number; y: number }
    params: Record<string, string>
  }>
  edges: Array<{
    id: string
    source: string
    sourceHandle?: string
    target: string
    targetHandle?: string
    kind: 'data' | 'parameter'
    parameter?: string
  }>
}

export interface NodeState {
  id: string
  status: string
  data?: string[]
  preview?: string[]
  error?: string
}

export interface WorkflowRun {
  id: string
  projectId: string
  projectVersion: number
  fromNodeId?: string
  status: string
  nodeStates: Record<string, NodeState>
  error?: string
}
