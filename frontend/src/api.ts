import type { Artifact, ComponentDef, Project, RunResult, SavedProjectResponse, Validation } from "./types";

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
  } catch (error) {
    throw new Error("network", { cause: error });
  }
  if (!response.ok && response.status !== 422) {
    throw new Error("http " + response.status);
  }
  return response.json() as Promise<T>;
}

export const api = {
  components: () => request<ComponentDef[]> ("/api/components"),
  saveProject: (project: Project) => request<SavedProjectResponse | Validation>("/api/projects", { method: "POST", body: JSON.stringify(project) }),
  loadProject: (id: string) => request<{ project: Project; artifacts: Record<string, Artifact> }>("/api/projects/" + id),
  run: (id: string, startNodeIds: string[]) => request<RunResult>("/api/projects/" + id + "/run", { method: "POST", body: JSON.stringify({ startNodeIds }) })
};
