export type Project = {
  format: "dataflow.project/v1";
  id: string;
  name: string;
  revision: number;
  nodes: ProjectNode[];
  edges: ProjectEdge[];
};
export type ProjectNode = {
  id: string;
  type: string;
  name: string;
  position: { x: number; y: number };
  params: Record<string, unknown>;
};
export type ProjectEdge = {
  id: string;
  source: string;
  target: string;
  sourcePort: string;
  targetPort: string;
};
export type ComponentField = {
  id: string;
  name: string;
  type: "string" | "text" | "select";
  required?: boolean;
  description?: string;
  options?: string[];
};
export type Port = { id: string; name: string; multi?: boolean };
export type ComponentDef = {
  type: string;
  name: string;
  category: string;
  description: string;
  inputs: Port[];
  outputs: Port[];
  params: ComponentField[];
};
export type Issue = { code: string; message: string; nodeId?: string; edgeId?: string };
export type Validation = {
  ok: boolean;
  issues: Issue[];
  executionOrder: string[];
  affectedNodeIds: string[];
};
export type Artifact = {
  nodeId: string;
  version: string;
  runId: string;
  projectVersion?: string;
  path?: string;
  content?: string;
  rows: Record<string, unknown>[];
  summary: string;
};
export type NodeRunResult = {
  nodeId: string;
  status: "succeeded" | "failed" | "blocked" | "reused";
  artifact?: Artifact;
  error?: string;
  reusedFrom?: string;
  basis?: string;
};
export type RunResult = {
  ok: boolean;
  runId: string;
  projectVersion: string;
  affectedNodeIds: string[];
  results: Record<string, NodeRunResult>;
  issues?: Issue[];
};
export type SavedProjectResponse = { project: Project; validation: Validation };
export type LoadedProjectResponse = {
  project: Project;
  artifacts: Record<string, Artifact>;
  latestRun?: RunResult;
  hasRun: boolean;
  projectVersion: string;
};
