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
export type NodeRunResult = {
  nodeId: string;
  status: "succeeded" | "failed" | "blocked" | "reused";
  artifact?: Artifact;
  error?: string;
  reason?: string;
  reusedFrom?: string;
  cause?: "reused" | "rerun_full" | "rerun_params" | "rerun_input" | "rerun_forced" | "rerun_first";
};
export type RunResult = {
  ok: boolean;
  runId: string;
  projectVersion: string;
  affectedNodeIds: string[];
  results: Record<string, NodeRunResult>;
  deliveries?: Record<string, DeliveryState>;
  issues?: Issue[];
};
export type SavedProjectResponse = { project: Project; validation: Validation };
export type Delivery = {
  nodeId: string;
  receiver: string;
  version: string;
  runId: string;
  projectVersion?: string;
  rows: number;
  empty: boolean;
  summary: string;
  stale?: boolean;
  staleReason?: string;
};
export type DeliveryEvent = { kind: "computed" | "superseded" | "published" | "invalidated" | "rejected"; version: string; runId: string; note: string };
export type ExpectedCandidate = { nodeId: string; version: string; runId: string };
export type DeliveryState = {
  nodeId: string;
  receiver: string;
  pending?: Delivery;
  published?: Delivery;
  history: DeliveryEvent[];
};
