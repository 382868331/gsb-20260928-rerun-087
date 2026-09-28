import type { WorkflowProject, WorkflowRun } from './types'

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...init
  })
  if (!response.ok) {
    const text = await response.text()
    throw new Error(text || response.statusText)
  }
  return response.json()
}

export const api = {
  saveProject: (project: WorkflowProject) =>
    request<{ project: WorkflowProject }>('/api/projects', {
      method: 'POST',
      body: JSON.stringify(project)
    }),
  getProject: (id: string) => request<WorkflowProject>('/api/projects/' + encodeURIComponent(id)),
  startRun: (projectId: string, fromNodeId?: string) =>
    request<WorkflowRun>('/api/runs', {
      method: 'POST',
      body: JSON.stringify({ projectId, fromNodeId })
    }),
  decide: (runId: string, approved: boolean, comment: string) =>
    request<WorkflowRun>('/api/runs/' + encodeURIComponent(runId) + '/decision', {
      method: 'POST',
      body: JSON.stringify({ approved, comment })
    })
}
