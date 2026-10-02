import type { ComponentDef, LoadedProjectResponse, Project, PublishPolicy, RunResult, SavedProjectResponse, Validation } from "./types";

export class ApiError extends Error {
  constructor(public status: number, message: string, public recoverable = true) {
    super(message);
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
  } catch {
    throw new ApiError(0, "无法连接后端", true);
  }
  let payload: unknown = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (!response.ok) {
    const message = (payload as { error?: string } | null)?.error || `请求失败（HTTP ${response.status}）`;
    throw new ApiError(response.status, message, response.status >= 500 || response.status === 0);
  }
  return payload as T;
}

export const api = {
  components: () => request<ComponentDef[]> ("/api/components"),
  saveProject: (project: Project) => request<SavedProjectResponse | Validation>("/api/projects", { method: "POST", body: JSON.stringify(project) }),
  loadProject: (id: string) => request<LoadedProjectResponse>("/api/projects/" + id),
  run: (id: string, startNodeIds: string[], publishPolicy: PublishPolicy) => request<RunResult>("/api/projects/" + id + "/run", { method: "POST", body: JSON.stringify({ startNodeIds, publishPolicy }) })
};
