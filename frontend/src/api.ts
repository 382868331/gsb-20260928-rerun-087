import type { Artifact, CandidateFilter, CandidateView, CompareView, Comparison, ComponentDef, DeliveryState, ExpectedCandidate, Project, RunResult, SavedProjectResponse, Validation } from "./types";

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
  const body = await response.json().catch(() => ({}));
  // 校验失败（422）会带回 issues / ok 字段，交给调用方展示；
  // 其它 HTTP 错误（500、404 等）直接抛出，避免被误当成“校验未通过”。
  if (!response.ok && !("issues" in body) && !("project" in body)) {
    throw new Error(typeof body.error === "string" ? body.error : "请求失败（HTTP " + response.status + "）");
  }
  return body as T;
}

export const api = {
  components: () => request<ComponentDef[]> ("/api/components"),
  saveProject: (project: Project) => request<SavedProjectResponse | Validation>("/api/projects", { method: "POST", body: JSON.stringify(project) }),
  loadProject: (id: string) => request<{ project: Project; artifacts: Record<string, Artifact>; deliveries?: Record<string, DeliveryState> }>("/api/projects/" + id),
  run: (id: string, startNodeIds: string[]) => request<RunResult>("/api/projects/" + id + "/run", { method: "POST", body: JSON.stringify({ startNodeIds }) }),
  confirm: (id: string, body: { nodeIds?: string[]; all?: boolean; expected?: ExpectedCandidate[] }) => request<{ confirmed: string[]; deliveries: Record<string, DeliveryState> }>("/api/projects/" + id + "/confirm", { method: "POST", body: JSON.stringify(body) })
  ,
  candidates: (id: string, filter: CandidateFilter) => {
    const params = new URLSearchParams();
    if (filter.receiver) params.set("receiver", filter.receiver);
    if (filter.projectVersion) params.set("projectVersion", filter.projectVersion);
    if (filter.runFrom) params.set("runFrom", filter.runFrom);
    if (filter.runTo) params.set("runTo", filter.runTo);
    if (filter.status) params.set("status", filter.status);
    const query = params.toString();
    return request<{ candidates: CandidateView[] }>("/api/projects/" + id + "/candidates" + (query ? "?" + query : ""));
  },
  compare: (id: string, a: string, b: string) => request<Comparison>("/api/projects/" + id + "/compare", { method: "POST", body: JSON.stringify({ a, b }) }),
  compareViews: (id: string) => request<{ views: CompareView[] }>("/api/projects/" + id + "/compare-views"),
  saveCompareView: (id: string, view: CompareView) => request<{ views: CompareView[] }>("/api/projects/" + id + "/compare-views", { method: "POST", body: JSON.stringify(view) }),
  deleteCompareView: (id: string, name: string) => request<{ views: CompareView[] }>("/api/projects/" + id + "/compare-views/" + encodeURIComponent(name), { method: "DELETE" })
};
