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
  path?: string;
  content?: string;
  rows: Record<string, unknown>[];
  summary: string;
};
export type Publication = {
  nodeId: string;
  recipient: string;
  version: string;
  runId: string;
  path?: string;
  rows: Record<string, unknown>[];
  summary: string;
  publishedAt: string;
};
export type Visibility = "published" | "already" | "computed" | "previous" | "none";
export type PublishPolicy = "all" | "partial";
export type NodeRunResult = {
  nodeId: string;
  status: "succeeded" | "failed" | "blocked" | "reused";
  artifact?: Artifact;
  visibility?: Visibility;
  recipient?: string;
  published?: Publication;
  error?: string;
  reason?: string;
  reusedFrom?: string;
  cause?: "reused" | "rerun_full" | "rerun_params" | "rerun_input" | "rerun_forced" | "rerun_first";
};
export type RunResult = {
  ok: boolean;
  runId: string;
  projectVersion: string;
  publishPolicy?: PublishPolicy;
  affectedNodeIds: string[];
  results: Record<string, NodeRunResult>;
  publications?: Record<string, Publication>;
  issues?: Issue[];
};
export type SavedProjectResponse = { project: Project; validation: Validation; publications?: Record<string, Publication> };
export type LoadedProjectResponse = { project: Project; artifacts: Record<string, Artifact>; publications?: Record<string, Publication> };
export type RunRequest = { startNodeIds: string[]; publishPolicy: PublishPolicy };
