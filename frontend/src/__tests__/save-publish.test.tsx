import { test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import App from "../App";

const components = [
  { type: "file_input", name: "文件输入", category: "输入", description: "d", inputs: [], outputs: [{ id: "rows", name: "数据表" }], params: [{ id: "fileName", name: "文件名", type: "string" as const, required: true }] },
  { type: "transform", name: "转换", category: "处理", description: "d", inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [{ id: "rows", name: "数据表" }], params: [] },
  { type: "file_output", name: "文件输出", category: "输出", description: "d", inputs: [{ id: "rows", name: "数据表" }], outputs: [], params: [{ id: "recipient", name: "接收方", type: "string" as const }] }
];

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

test("blank project save with null collections stays editable and shows revision", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects" && init?.method === "POST") {
      // 模拟旧后端把空切片序列化成 null 的真实事故响应。
      return jsonResponse({ project: { format: "dataflow.project/v1", id: "blank-dataflow", name: "空工程", revision: 1, nodes: null, edges: null }, validation: { ok: true, issues: null, executionOrder: null, affectedNodeIds: null }, publications: {} });
    }
    return jsonResponse({}, 404);
  }));
  render(<App />);
  fireEvent.click(await screen.findByText("新建空工程"));
  fireEvent.click(screen.getByText("保存版本"));
  await waitFor(() => expect(document.querySelector(".revision")?.textContent).toContain("1"));
  expect(screen.getByRole("heading", { name: "数据处理流程设计器" })).toBeTruthy();
  // 保存成功后立即继续编辑：添加节点不应崩溃。
  fireEvent.click(screen.getByText("文件输入"));
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(screen.getByRole("heading", { name: "数据处理流程设计器" })).toBeTruthy();
  vi.unstubAllGlobals();
});

test("real save failure keeps canvas and input and offers recovery", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects" && init?.method === "POST") {
      return jsonResponse({ error: "模拟磁盘故障" }, 503);
    }
    return jsonResponse({}, 404);
  }));
  render(<App />);
  fireEvent.click(await screen.findByText("新建空工程"));
  fireEvent.click(screen.getByText("文件输入"));
  fireEvent.click(screen.getByText("保存版本"));
  expect(await screen.findByText(/保存\/运行请求失败（可恢复）/)).toBeTruthy();
  expect(document.querySelector(".save-banner")?.textContent).toContain("模拟磁盘故障");
  expect(document.querySelector(".revision")?.textContent).toContain("0");
  expect(screen.getByText("重试保存")).toBeTruthy();
  vi.unstubAllGlobals();
});

const multiProject = {
  format: "dataflow.project/v1", id: "multi", name: "多接收方", revision: 1,
  nodes: [
    { id: "in", type: "file_input", name: "输入", position: { x: 0, y: 0 }, params: { fileName: "a.csv", format: "csv", content: "id,city\n1,S" } },
    { id: "o1", type: "file_output", name: "甲", position: { x: 1, y: 0 }, params: { path: "jia.json", format: "json", recipient: "甲" } },
    { id: "o2", type: "file_output", name: "乙", position: { x: 1, y: 1 }, params: { path: "yi.csv", format: "csv", recipient: "乙" } }
  ],
  edges: [
    { id: "e1", source: "in", target: "o1", sourcePort: "rows", targetPort: "rows" },
    { id: "e2", source: "in", target: "o2", sourcePort: "rows", targetPort: "rows" }
  ]
};

function partialFailureRun() {
  return {
    ok: false, runId: "run-2", projectVersion: "v", publishPolicy: "partial", affectedNodeIds: [],
    results: {
      in: { nodeId: "in", status: "succeeded" },
      o1: { nodeId: "o1", status: "succeeded", visibility: "published", recipient: "甲", published: { nodeId: "o1", recipient: "甲", version: "new", runId: "run-2", rows: [{ id: "1" }], summary: "1 行", publishedAt: "now", path: "/tmp/jia.json" }, artifact: { nodeId: "o1", version: "new", runId: "run-2", rows: [{ id: "1" }], summary: "1 行", path: "/tmp/jia.json" }, reason: "已先行向甲发布" },
      o2: { nodeId: "o2", status: "failed", visibility: "previous", recipient: "乙", published: { nodeId: "o2", recipient: "乙", version: "old", runId: "run-1", rows: [], summary: "旧证据", publishedAt: "before" }, error: "发布失败", reason: "乙仍可见旧版" }
    },
    publications: {
      o1: { nodeId: "o1", recipient: "甲", version: "new", runId: "run-2", rows: [{ id: "1" }], summary: "1 行", publishedAt: "now", path: "/tmp/jia.json" },
      o2: { nodeId: "o2", recipient: "乙", version: "old", runId: "run-1", rows: [], summary: "旧证据", publishedAt: "before" }
    }
  };
}

test("partial failure explains computed vs visible and keeps prior evidence after withdraw", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects" && init?.method === "POST") return jsonResponse({ project: multiProject, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } });
    if (String(url).includes("/run")) return jsonResponse(partialFailureRun());
    return jsonResponse({}, 404);
  }));
  render(<App />);
  // 初始是默认样例：先新建空工程，再让保存返回多接收方工程。
  fireEvent.click(await screen.findByText("新建空工程"));
  fireEvent.change(document.querySelector(".policy select")!, { target: { value: "partial" } });
  fireEvent.click(screen.getByText(/整流程运行/));
  expect(await screen.findByText(/1 个已发布、0 个仅已计算/)).toBeTruthy();
  const rows = document.querySelectorAll(".reuse-row");
  const o2Row = Array.from(rows).find((row) => row.textContent?.includes("→ 乙"))!;
  fireEvent.click(o2Row.querySelector("span")!);
  expect(screen.getAllByText(/接收方仍见旧版/).length).toBeGreaterThan(0);
  expect(screen.getByText(/run-1/)).toBeTruthy();
  const o1Row = Array.from(rows).find((row) => row.textContent?.includes("→ 甲"))!;
  fireEvent.click(o1Row.querySelector("span")!);
  expect(screen.getAllByText(/已发布·接收方可见/).length).toBeGreaterThan(0);
  // 撤回未发布修订后，已发出的甲版本证据仍然保留。
  // （运行失败的结果本身不是已发布快照；模拟一次编辑产生未发布修订。）
  fireEvent.click(document.querySelector(".reuse-row[data-node-id=\"o1\"]")!);
  const nameInput = screen.getAllByDisplayValue("甲").find((element) => element.tagName === "INPUT")!;
  fireEvent.change(nameInput, { target: { value: "甲改" } });
  fireEvent.click(screen.getByText("撤回未发布修订"));
  fireEvent.click(o1Row.querySelector("span")!);
  expect(screen.getAllByText(/run-2/).length).toBeGreaterThan(0);
  vi.unstubAllGlobals();
});
