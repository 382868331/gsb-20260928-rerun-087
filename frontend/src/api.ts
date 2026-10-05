import type { CandidateComparisonState, ComponentDef, DeliveryState, ExpectedCandidate, GroupResponse, LoadProjectResponse, Project, PublishGroupDependency, PublishGroupMember, RunApiResponse, SavedProjectResponse, Validation } from "./types";

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
  const body = await response.json().catch(() => ({}));
  // 校验失败（422）会带回 issues / ok 字段，交给调用方展示；
  // 其它 HTTP 错误（500、404 等）直接抛出，避免被误当成“校验未通过”。
  if (!response.ok && !("issues" in body) && !("project" in body) && !("groups" in body)) {
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
  prepareGroup: (id: string, body: { members: PublishGroupMember[]; dependencies: PublishGroupDependency[] }) => request<GroupResponse>("/api/projects/" + id + "/groups", { method: "POST", body: JSON.stringify(body) }),
  commitGroup: (id: string, groupId: string, unavailableReceivers: string[]) => request<GroupResponse>("/api/projects/" + id + "/groups/" + groupId + "/commit", { method: "POST", body: JSON.stringify({ unavailableReceivers }) }),
  recoverGroup: (id: string, groupId: string) => request<GroupResponse>("/api/projects/" + id + "/groups/" + groupId + "/recover", { method: "POST", body: "{}" }),
  cancelGroup: (id: string, groupId: string) => request<GroupResponse>("/api/projects/" + id + "/groups/" + groupId + "/cancel", { method: "POST", body: "{}" })
};
