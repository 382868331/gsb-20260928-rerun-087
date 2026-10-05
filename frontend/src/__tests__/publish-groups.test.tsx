import { test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import App from "../App";

const components = [
  { type: "file_input", name: "文件输入", category: "输入", description: "d", inputs: [], outputs: [{ id: "rows", name: "数据表" }], params: [{ id: "fileName", name: "文件名", type: "string", required: true }] },
  { type: "publish", name: "发布到接收方", category: "发布", description: "d", inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [], params: [{ id: "receiver", name: "接收方", type: "string", required: true }] }
];

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const pendingDeliveries = {
  "publish-1": { nodeId: "publish-1", receiver: "财务", pending: { nodeId: "publish-1", receiver: "财务", version: "v-fin", runId: "run-1", rows: 2, empty: false, summary: "s" }, history: [] },
  "publish-2": { nodeId: "publish-2", receiver: "运营", pending: { nodeId: "publish-2", receiver: "运营", version: "v-ops", runId: "run-1", rows: 3, empty: false, summary: "s" }, history: [] }
};

const preparedGroup = {
  id: "grp-1",
  members: [
    { nodeId: "publish-1", version: "v-fin", runId: "run-1" },
    { nodeId: "publish-2", version: "v-ops", runId: "run-1" }
  ],
  dependencies: [
    { nodeId: "publish-1", dependsOn: "publish-2", version: "v-ops" },
    { nodeId: "publish-2", dependsOn: "publish-1", version: "v-fin" }
  ],
  status: "prepared",
  basis: { "publish-1": "", "publish-2": "" },
  createdAt: "2026-10-06T00:00:00Z"
};

const publishedDeliveries = {
  "publish-1": { ...pendingDeliveries["publish-1"], published: pendingDeliveries["publish-1"].pending, pending: undefined },
  "publish-2": { ...pendingDeliveries["publish-2"], published: pendingDeliveries["publish-2"].pending, pending: undefined }
};

function runResponse() {
  return jsonResponse({
    run: { ok: true, runId: "run-1", projectVersion: "pv", affectedNodeIds: [], results: {}, deliveries: pendingDeliveries },
    candidateArchives: []
  });
}

function saveResponse(init?: RequestInit) {
  const body = JSON.parse(String(init?.body));
  return jsonResponse({ project: { ...body, revision: 1 }, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } });
}

afterEach(() => vi.unstubAllGlobals());

async function runFlow() {
  render(<App />);
  await screen.findByText("文件输入");
  fireEvent.click(screen.getByText("整流程运行"));
  await waitFor(() => expect(screen.getByText("运行成功。")).toBeTruthy(), { timeout: 5000 });
}

test("发布组：组成后提交期间接收方不可用，恢复后整组完成并保留采用身份", async () => {
  let committed = false;
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") return saveResponse(init);
    if (url.endsWith("/run")) return runResponse();
    if (url.endsWith("/groups")) return jsonResponse({ group: preparedGroup, groups: { "grp-1": preparedGroup } });
    if (url.endsWith("/groups/grp-1/commit") && !committed) {
      const committing = { ...preparedGroup, status: "committing", note: "接收方 publish-2 暂时不可用，整组停留在提交中" };
      return jsonResponse({ error: "接收方 publish-2 暂时不可用：整组保持提交中，未发布任何结果", group: committing, groups: { "grp-1": committing }, deliveries: pendingDeliveries }, 503);
    }
    if (url.endsWith("/groups/grp-1/recover") || url.endsWith("/groups/grp-1/commit")) {
      committed = true;
      const done = {
        ...preparedGroup, status: "committed", note: "整组已共同确认，所有接收方同时可见新版本",
        adopted: {
          "publish-1": { nodeId: "publish-1", version: "v-fin", runId: "run-1" },
          "publish-2": { nodeId: "publish-2", version: "v-ops", runId: "run-1" }
        }
      };
      return jsonResponse({ group: done, groups: { "grp-1": done }, deliveries: publishedDeliveries, candidateArchives: [] });
    }
    return jsonResponse({}, 404);
  }));
  await runFlow();
  // 组成发布组：默认选中全部待确认候选。
  fireEvent.click(screen.getByText(/组成发布组/));
  await waitFor(() => expect(screen.getByText("准备中（尚未提交，可整组取消）")).toBeTruthy(), { timeout: 5000 });
  // 模拟接收方 运营 暂时不可用，提交整组：整组停留在提交中，不发布任何成员。
  fireEvent.change(screen.getByLabelText("模拟不可用-grp-1"), { target: { value: "publish-2" } });
  fireEvent.click(screen.getByText("提交整组"));
  await waitFor(() => expect(screen.getByText("提交中（中断后必须恢复到确定结局）")).toBeTruthy(), { timeout: 5000 });
  expect(screen.getAllByText(/接收方 publish-2 暂时不可用/).length).toBeGreaterThan(0);
  // 已经进入提交的组不能取消，只能恢复。
  expect(screen.queryByText("取消整组")).toBeNull();
  // 恢复：到达确定结局，展示每个接收方实际采用的候选身份。
  fireEvent.click(screen.getByText("恢复提交"));
  await waitFor(() => expect(screen.getByText("已完成（整组对接收方可见）")).toBeTruthy(), { timeout: 5000 });
  expect(screen.getByText(/每个接收方实际采用的候选身份/)).toBeTruthy();
  expect(screen.getByText(/财务：版本 v-fin/)).toBeTruthy();
  expect(screen.getByText(/运营：版本 v-ops/)).toBeTruthy();
});

test("发布组：尚未提交可以整组取消，取消后不能再提交", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") return saveResponse(init);
    if (url.endsWith("/run")) return runResponse();
    if (url.endsWith("/groups")) return jsonResponse({ group: preparedGroup, groups: { "grp-1": preparedGroup } });
    if (url.endsWith("/groups/grp-1/cancel")) {
      const cancelled = { ...preparedGroup, status: "cancelled", note: "用户在提交前取消整组，未发布任何结果" };
      return jsonResponse({ group: cancelled, groups: { "grp-1": cancelled } });
    }
    return jsonResponse({}, 404);
  }));
  await runFlow();
  fireEvent.click(screen.getByText(/组成发布组/));
  await waitFor(() => expect(screen.getByText("准备中（尚未提交，可整组取消）")).toBeTruthy(), { timeout: 5000 });
  fireEvent.click(screen.getByText("取消整组"));
  await waitFor(() => expect(screen.getByText("已取消（未发布任何结果）")).toBeTruthy(), { timeout: 5000 });
  expect(screen.queryByText("提交整组")).toBeNull();
});

test("发布组：过期候选混入时后端拒绝，整组保持未发布", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") return saveResponse(init);
    if (url.endsWith("/run")) return runResponse();
    if (url.endsWith("/groups")) return jsonResponse({ error: "节点 publish-2 的候选输入依据已失效：来源运行失败，整组保持未发布", groups: {} }, 409);
    return jsonResponse({}, 404);
  }));
  await runFlow();
  fireEvent.click(screen.getByText(/组成发布组/));
  await waitFor(() => expect(screen.getByText(/发布组操作未完成/)).toBeTruthy(), { timeout: 5000 });
  expect(screen.getByText(/整组保持未发布/)).toBeTruthy();
});
