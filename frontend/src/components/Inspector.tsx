import type { ComponentDef, Project, ProjectNode } from "../types";

type Props = {
  project: Project;
  node?: ProjectNode;
  component?: ComponentDef;
  staleIds: Set<string>;
  onPatchNode: (id: string, patch: Partial<ProjectNode>) => void;
  onPatchParam: (id: string, key: string, value: unknown) => void;
  onRunFrom: () => void;
};

export function Inspector({ project, node, component, staleIds, onPatchNode, onPatchParam, onRunFrom }: Props) {
  if (!node || !component) {
    return <aside className="inspector"><h2>检查页面状态</h2><p>选择一个步骤，参数和数据依赖会分别显示。</p></aside>;
  }
  const params = component.params ?? [];
  const incoming = project.edges.filter((edge) => edge.target === node.id);
  const outgoing = project.edges.filter((edge) => edge.source === node.id);
  const nameById = new Map(project.nodes.map((item) => [item.id, item.name]));
  return (
    <aside className="inspector">
      <h2>{node.name}</h2>
      <p className="muted">{component.description}</p>
      {staleIds.has(node.id) && <div className="banner warning">本步骤或其上游已修改：旧结果已标记失效。</div>}
      <button className="primary" onClick={onRunFrom}>从本步骤局部重跑下游</button>

      <h3>参数（不是数据依赖）</h3>
      <label>步骤名称<input value={node.name} onChange={(event) => onPatchNode(node.id, { name: event.target.value })} /></label>
      {params.map((field) => (
        <label key={field.id}>
          {field.name}{field.required ? " *" : ""}
          {field.type === "text" ? (
            <textarea value={String(node.params[field.id] ?? "")} onChange={(event) => onPatchParam(node.id, field.id, event.target.value)} rows={7} />
          ) : field.type === "select" ? (
            <select value={String(node.params[field.id] ?? "")} onChange={(event) => onPatchParam(node.id, field.id, event.target.value)}>
              {field.options?.map((option) => <option key={option} value={option}>{option}</option>)}
            </select>
          ) : (
            <input value={String(node.params[field.id] ?? "")} onChange={(event) => onPatchParam(node.id, field.id, event.target.value)} />
          )}
          {field.description && <small>{field.description}</small>}
        </label>
      ))}

      <h3>实际数据依赖</h3>
      <div className="ports">
        <b>输入端口：</b>
        {incoming.length === 0 ? <span className="muted">无</span> : incoming.map((edge) => <span key={edge.id}>{nameById.get(edge.source)}.{edge.sourcePort} → {edge.targetPort}</span>)}
        <b>输出端口：</b>
        {outgoing.length === 0 ? <span className="muted">无</span> : outgoing.map((edge) => <span key={edge.id}>{edge.sourcePort} → {nameById.get(edge.target)}.{edge.targetPort}</span>)}
      </div>
    </aside>
  );
}
