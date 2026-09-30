import type { Artifact, ComponentDef, LoadedProjectResponse, Project, RunResult, SavedProjectResponse, Validation } from "./types";

type Init = Parameters<typeof fetch>[1];

async function request<T>(url: string, init?: Init): Promise<T> {
	let response: Response;
	try {
		response = await fetch(url, { headers: { "Content-Type": "application/json" }, ...init });
	} catch {
		throw new Error("无法连接后端服务，请确认 Go 服务已启动");
	}
	if (!response.ok) {
		const payload = await response.json().catch(() => ({})) as { error?: string; validation?: Validation };
		if (payload.validation) return payload as T;
		throw new Error(payload.error ?? ("请求失败：HTTP " + response.status));
	}
	return response.json() as Promise<T>;
}

export const api = {
	components: () => request<ComponentDef[]> ("/api/components"),
	saveProject: (project: Project) => request<SavedProjectResponse | Validation>("/api/projects", { method: "POST", body: JSON.stringify(project) }),
	loadProject: async (id: string): Promise<LoadedProjectResponse> => {
		const projectResponse = await request<{ project: Project; artifacts: Record<string, Artifact>; projectVersion: string }>("/api/projects/" + id);
		try {
			const runResponse = await request<{ run?: RunResult; exists: boolean }>("/api/projects/" + id + "/runs/latest");
			return { ...projectResponse, latestRun: runResponse.exists ? runResponse.run : undefined, hasRun: runResponse.exists };
		} catch {
			return { ...projectResponse, hasRun: false };
		}
	},
	run: (id: string, startNodeIds: string[]) => request<RunResult>("/api/projects/" + id + "/run", { method: "POST", body: JSON.stringify({ startNodeIds }) })
};
