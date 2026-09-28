import type { Edge, Node } from '@xyflow/react'
import type { WorkflowNodeData, WorkflowProject } from './types'

let counter = 10

export function makeId(prefix: string) {
  counter += 1
  return prefix + '-' + Date.now().toString(36) + '-' + counter
}

export function emptyProject(): WorkflowProject {
  return {
    id: 'first-round-workflow',
    name: '文件输入—转换—输出',
    version: 0,
    nodes: [
      { id: 'input-1', type: 'file-input', name: '读取文件', position: { x: 40, y: 150 }, params: { content: 'alpha\nbeta\ngamma' } },
      { id: 'transform-1', type: 'transform', name: '大写转换', position: { x: 320, y: 150 }, params: { operation: 'uppercase', suffix: '' } },
      { id: 'output-1', type: 'file-output', name: '写出文件', position: { x: 600, y: 150 }, params: {} }
    ],
    edges: [
      { id: 'data-in-transform', source: 'input-1', sourceHandle: 'data-out', target: 'transform-1', targetHandle: 'data-in', kind: 'data' },
      { id: 'data-transform-out', source: 'transform-1', sourceHandle: 'data-out', target: 'output-1', targetHandle: 'data-in', kind: 'data' }
    ]
  }
}

export function toReactFlow(project: WorkflowProject): { nodes: Node<WorkflowNodeData>[]; edges: Edge[] } {
  return {
    nodes: project.nodes.map((node) => ({
      id: node.id,
      type: 'workflow',
      position: node.position,
      data: { label: node.name, type: node.type, params: node.params }
    })),
    edges: project.edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      sourceHandle: edge.sourceHandle,
      target: edge.target,
      targetHandle: edge.targetHandle,
      data: { kind: edge.kind, parameter: edge.parameter },
      animated: edge.kind === 'data',
      label: edge.kind === 'parameter' ? '参数:' + (edge.parameter ?? '') : '数据',
      style: edge.kind === 'parameter' ? { stroke: '#7c3aed', strokeDasharray: '6 3' } : { stroke: '#0891b2' }
    }))
  }
}

export function fromReactFlow(name: string, version: number, nodes: Node<WorkflowNodeData>[], edges: Edge[]): WorkflowProject {
  return {
    id: 'first-round-workflow',
    name,
    version,
    nodes: nodes.map((node) => ({
      id: node.id,
      type: node.data.type,
      name: node.data.label,
      position: { x: node.position.x, y: node.position.y },
      params: node.data.params
    })),
    edges: edges.map((edge) => ({
      id: edge.id,
      source: edge.source,
      sourceHandle: edge.sourceHandle ?? undefined,
      target: edge.target,
      targetHandle: edge.targetHandle ?? undefined,
      kind: (edge.data?.kind === 'parameter' ? 'parameter' : 'data') as 'data' | 'parameter',
      parameter: typeof edge.data?.parameter === 'string' ? edge.data.parameter : undefined
    }))
  }
}

export function descendants(edges: Edge[], startId: string): Set<string> {
  const out = new Set<string>([startId])
  const queue = [startId]
  while (queue.length) {
    const id = queue.shift()!
    edges.filter((edge) => edge.source === id).forEach((edge) => {
      if (!out.has(edge.target)) {
        out.add(edge.target)
        queue.push(edge.target)
      }
    })
  }
  return out
}
