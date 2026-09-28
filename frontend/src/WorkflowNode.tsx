import { Handle, Position, type NodeProps } from '@xyflow/react'
import type { WorkflowNodeData } from './types'

const labels: Record<string, string> = {
  'file-input': '文件输入',
  transform: '转换',
  'file-output': '文件输出',
  manual: '人工分支'
}

const statusColor: Record<string, string> = {
  success: '#16a34a',
  failed: '#dc2626',
  waiting: '#d97706',
  stale: '#64748b'
}

export function WorkflowNode({ data, selected }: NodeProps) {
  const node = data as unknown as WorkflowNodeData
  const status = node.status ?? 'stale'
  return (
    <div className={'workflow-node ' + (selected ? 'selected' : '') + ' status-' + status}>
      <Handle type="target" position={Position.Left} id="data-in" style={{ top: 35, background: '#0891b2' }} />
      <Handle type="target" position={Position.Left} id="parameter-in" style={{ top: 65, background: '#7c3aed' }} />
      <div className="node-kind">{labels[node.type]}</div>
      <strong>{node.label}</strong>
      <small>{node.stale ? '本轮结果已失效' : status}</small>
      {node.error && <em>{node.error}</em>}
      <Handle type="source" position={Position.Right} id="data-out" style={{ top: 35, background: '#0891b2' }} />
      <Handle type="source" position={Position.Right} id="parameter-out" style={{ top: 65, background: '#7c3aed' }} />
    </div>
  )
}
