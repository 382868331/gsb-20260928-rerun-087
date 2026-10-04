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

afterEach(() => vi.unstubAllGlobals());

test("空工程保存后即使节点集合为 null 也能继续编辑", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") {
      const body = JSON.parse(String(init?.body));
      // 模拟旧后端/旧存档：空集合序列化为 null。
      return jsonResponse({ project: { ...body, revision: 1, nodes: null, edges: null }, validation: { ok: true, issues: null, executionOrder: [], affectedNodeIds: null } });
    }
    return jsonResponse({}, 404);
  }));
  render(<App />);
  await screen.findByText("文件输入");
  fireEvent.click(screen.getByText("新建空工程"));
  fireEvent.click(screen.getByText("保存版本"));
  await waitFor(() => expect(screen.getByText(/已保存为 revision 1，可立即继续编辑/)).toBeTruthy());
  // 保存成功后可以立即继续编辑：添加组件不崩溃。
  fireEvent.click(screen.getByText("文件输入"));
  expect(screen.getByRole("heading", { name: "数据处理流程设计器" })).toBeTruthy();
});

test("保存校验失败立即展示原因且保留画布", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") return jsonResponse({ ok: false, issues: [{ code: "missing_parameter", message: "读取订单 CSV 缺少参数：文件名" }], executionOrder: [], affectedNodeIds: [] }, 422);
    return jsonResponse({}, 404);
  }));
  render(<App />);
  await screen.findByText("文件输入");
  fireEvent.click(screen.getByText("保存版本"));
  await waitFor(() => expect(screen.getByText(/校验未通过/)).toBeTruthy());
  expect(screen.getByText(/缺少参数：文件名/)).toBeTruthy();
  // 画布保留：示例步骤仍在。
  expect(screen.getAllByText("读取订单 CSV").length).toBeGreaterThan(0);
});

test("保存遇到服务器错误时提示可恢复状态", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") return jsonResponse({ error: "磁盘写入失败" }, 500);
    return jsonResponse({}, 404);
  }));
  render(<App />);
  await screen.findByText("文件输入");
  fireEvent.click(screen.getByText("保存版本"));
  await waitFor(() => expect(screen.getByText(/保存失败：磁盘写入失败/)).toBeTruthy());
  expect(screen.getByText(/可直接重试/)).toBeTruthy();
});

test("多接收方发布：运行后仅已计算，确认后对接收方可见", async () => {
  const deliveries = {
    "publish-1": { nodeId: "publish-1", receiver: "财务", pending: { nodeId: "publish-1", receiver: "财务", version: "v1", runId: "run-1", rows: 2, empty: false, summary: "s" }, history: [{ kind: "computed", version: "v1", runId: "run-1", note: "仅已计算" }] },
    "publish-2": { nodeId: "publish-2", receiver: "运营", pending: { nodeId: "publish-2", receiver: "运营", version: "v2", runId: "run-1", rows: 0, empty: true, summary: "s" }, history: [] }
  };
  const confirmedDeliveries = {
    "publish-1": { ...deliveries["publish-1"], published: deliveries["publish-1"].pending, pending: undefined },
    "publish-2": { ...deliveries["publish-2"], published: deliveries["publish-2"].pending, pending: undefined }
  };
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects") {
      const body = JSON.parse(String(init?.body));
      return jsonResponse({ project: { ...body, revision: 1 }, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } });
    }
    if (url.endsWith("/run")) return jsonResponse({ ok: true, runId: "run-1", projectVersion: "p", affectedNodeIds: [], results: {}, deliveries });
    if (url.endsWith("/confirm")) {
      const confirmBody = JSON.parse(String(init?.body));
      // 共同确认必须携带页面实际看到的候选版本与运行批次。
      expect(confirmBody.all).toBe(true);
      expect(confirmBody.targets).toEqual([
        { nodeId: "publish-1", version: "v1", runId: "run-1" },
        { nodeId: "publish-2", version: "v2", runId: "run-1" }
      ]);
      return jsonResponse({ confirmed: ["publish-1", "publish-2"], deliveries: confirmedDeliveries });
    }
    return jsonResponse({}, 404);
  }));
  render(<App />);
  await screen.findByText("文件输入");
  fireEvent.click(screen.getByText("整流程运行"));
  await waitFor(() => expect(screen.getAllByText(/仅已计算/).length).toBeGreaterThan(0));
  expect(screen.getByText(/空表（0 行）/)).toBeTruthy();
  fireEvent.click(screen.getByText(/全部结果共同确认/));
  await waitFor(() => expect(screen.getAllByText("已对接收方可见").length).toBe(2));
});
