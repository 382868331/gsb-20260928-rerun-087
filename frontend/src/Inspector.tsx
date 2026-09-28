import type { Edge, Node } from '@xyflow/react'
import type { WorkflowNodeData, WorkflowRun } from './types'

interface Props {
  node: Node<WorkflowNodeData> | null
  nodes: Node<WorkflowNodeData>[]
  edges: Edge[]
  run: WorkflowRun | null
  onUpdate: (node: Node<WorkflowNodeData>) => void
  onAdd: (type: WorkflowNodeData['type']) => void
}

export function Inspector({ node, edges, run, onUpdate, onAdd }: Props) {
  const state = node ? run?.nodeStates[node.id] : undefined
  return (
    <aside className="inspector">
      <h2>组件与参数</h2>
      <div className="button-row">
        <button onClick={() => onAdd('file-input')}>输入</button>
        <button onClick={() => onAdd('transform')}>转换</button>
        <button onClick={() => onAdd('manual')}>人工</button>
        <button onClick={() => onAdd('file-output')}>输出</button>
      </div>
      <p className="hint">青色手柄拖出数据连线；紫色手柄拖出参数连线。</p>
      {node ? (
        <section>
          <label>名称<input value={node.data.label} onChange={(e) => onUpdate({ ...node, data: { ...node.data, label: e.target.value } })} /></label>
          {node.data.type === 'file-input' && (
            <label>文件内容<textarea rows={7} value={node.data.params.content ?? ''} onChange={(e) => onUpdate({ ...node, data: { ...node.data, params: { ...node.data.params, content: e.target.value } } })} /></label>
          )}
          {node.data.type === 'transform' && (
            <>
              <label>操作
                <select value={node.data.params.operation ?? 'uppercase'} onChange={(e) => onUpdate({ ...node, data: { ...node.data, params: { ...node.data.params, operation: e.target.value } } })}>
                  <option value="uppercase">大写</option>
                  <option value="lowercase">小写</option>
                  <option value="trim">去空白</option>
                  <option value="reverse-lines">行逆序</option>
                </select>
              </label>
              <label>后缀<input value={node.data.params.suffix ?? ''} onChange={(e) => onUpdate({ ...node, data: { ...node.data, params: { ...node.data.params, suffix: e.target.value } } })} /></label>
            </>
          )}
          <h3>页面状态</h3>
          <div className={'pill ' + (state?.status ?? 'stale')}>{state?.status ?? '未运行/失效'}</div>
          {state?.error && <p className="error">{state.error}</p>}
          <h3>实际数据</h3>
          <pre>{(state?.preview ?? ['暂无本轮结果']).join('\n')}</pre>
          <h3>连线</h3>
          {edges.filter((edge) => edge.source === node.id || edge.target === node.id).map((edge) => (
            <div className="edge-row" key={edge.id}>{edge.data?.kind === 'parameter' ? '参数' : '数据'}：{edge.source} → {edge.target}{edge.data?.parameter ? '（' + edge.data.parameter + '）' : ''}</div>
          ))}
        </section>
      ) : <p>选择节点查看状态、数据和局部重跑范围。</p>}
    </aside>
  )
}
