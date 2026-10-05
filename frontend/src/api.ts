import type { CandidateComparisonState, ComponentDef, DeliveryState, ExpectedCandidate, LoadProjectResponse, Project, PublishGroupDependency, PublishGroupResponse, RunApiResponse, SavedProjectResponse, Validation } from "./types";

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
  const body = await response.json().catch(() => ({}));
  // 校验失败（422）会带回 issues / ok 字段，交给调用方展示；
  // 其它 HTTP 错误（500、404 等）直接抛出，避免被误当成“校验未通过”。
  // 发布组冲突 / 中止（409、422）会带回 group / publishGroups，交给调用方展示确定结局。
  if (!response.ok && !("issues" in body) && !("project" in body) && !("publishGroups" in body)) {
    throw new Error(typeof body.error === "string" ? body.error : "请求失败（HTTP " + response.status + "）");
  }
  return body as T;
}

export const api = {
  components: () => request<ComponentDef[]> ("/api/components"),
  saveProject: (project: Project) => request<SavedProjectResponse | Validation>("/api/projects", { method: "POST", body: JSON.stringify(project) }),
  loadProject: (id: string) => request<LoadProjectResponse>("/api/projects/" + id),
  run: (id: string, startNodeIds: string[]) => request<RunApiResponse>("/api/projects/" + id + "/run", { method: "POST", body: JSON.stringify({ startNodeIds }) }),
  confirm: (id: string, body: { nodeIds?: string[]; all?: boolean; expected?: ExpectedCandidate[] }) => request<{ confirmed: string[]; deliveries: Record<string, DeliveryState>; candidateArchives?: import("./types").CandidateArchive[] }>("/api/projects/" + id + "/confirm", { method: "POST", body: JSON.stringify(body) }),
  saveComparison: (id: string, comparison: CandidateComparisonState) => request<{ comparison: CandidateComparisonState }>("/api/projects/" + id + "/candidate-comparison", { method: "PUT", body: JSON.stringify(comparison) })
  ,
  createPublishGroup: (id: string, body: { nodeIds: string[]; dependencies: PublishGroupDependency[] }) => request<PublishGroupResponse>("/api/projects/" + id + "/publish-groups", { method: "POST", body: JSON.stringify(body) }),
  confirmPublishGroup: (id: string, groupId: string) => request<PublishGroupResponse>("/api/projects/" + id + "/publish-groups/" + groupId + "/confirm", { method: "POST", body: "{}" }),
  cancelPublishGroup: (id: string, groupId: string) => request<PublishGroupResponse>("/api/projects/" + id + "/publish-groups/" + groupId + "/cancel", { method: "POST", body: "{}" }),
  setReceiverAvailability: (id: string, receiver: string, available: boolean) => request<{ receiver: string; available: boolean }>("/api/projects/" + id + "/receivers/" + encodeURIComponent(receiver) + "/availability", { method: "PUT", body: JSON.stringify({ available }) })
};
