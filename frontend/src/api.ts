import type { Artifact, ComponentDef, ConfirmTarget, DeliveryState, Project, RunResult, SavedProjectResponse, Validation } from "./types";

export class ApiError extends Error {
  deliveries?: Record<string, DeliveryState>;
  constructor(message: string, deliveries?: Record<string, DeliveryState>) {
    super(message);
    this.deliveries = deliveries;
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
  const body = await response.json().catch(() => ({}));
  // 校验失败（422）会带回 issues / ok 字段，交给调用方展示；
  // 其它 HTTP 错误（500、404 等）直接抛出，避免被误当成“校验未通过”。
  if (!response.ok && !("issues" in body) && !("project" in body)) {
    const message = typeof body.error === "string" ? body.error : "请求失败（HTTP " + response.status + "）";
    throw new ApiError(message, (body as { deliveries?: Record<string, DeliveryState> }).deliveries);
  }
  return body as T;
}

export const api = {
  components: () => request<ComponentDef[]> ("/api/components"),
  saveProject: (project: Project) => request<SavedProjectResponse | Validation>("/api/projects", { method: "POST", body: JSON.stringify(project) }),
  loadProject: (id: string) => request<{ project: Project; artifacts: Record<string, Artifact>; deliveries?: Record<string, DeliveryState> }>("/api/projects/" + id),
  run: (id: string, startNodeIds: string[]) => request<RunResult>("/api/projects/" + id + "/run", { method: "POST", body: JSON.stringify({ startNodeIds }) }),
  confirm: (id: string, body: { targets?: ConfirmTarget[]; all?: boolean }) => request<{ confirmed: string[]; deliveries: Record<string, DeliveryState> }>("/api/projects/" + id + "/confirm", { method: "POST", body: JSON.stringify(body) })
};
