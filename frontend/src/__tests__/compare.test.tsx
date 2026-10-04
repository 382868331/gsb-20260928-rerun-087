import { test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import App from "../App";

const components = [
  { type: "file_input", name: "文件输入", category: "输入", description: "d", inputs: [], outputs: [{ id: "rows", name: "数据表" }], params: [{ id: "fileName", name: "文件名", type: "string", required: true }] },
  { type: "publish", name: "发布到接收方", category: "发布", description: "d", inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [], params: [{ id: "receiver", name: "接收方", type: "string", required: true }] }
];

const candidates = [
  { key: "publish-1|v1|run-1", nodeId: "publish-1", receiver: "财务", version: "v1", runId: "run-1", projectVersion: "rev-a", status: "superseded", rows: 2, empty: false, summary: "s" },
  { key: "publish-1|v2|run-2", nodeId: "publish-1", receiver: "财务", version: "v2", runId: "run-2", projectVersion: "rev-b", status: "pending", rows: 3, empty: false, summary: "s" }
];

const comparison = {
  a: candidates[0],
  b: candidates[1],
  added: [{ id: "3", city: "S" }],
  removed: [{ id: "2", city: "B" }],
  changes: [{ rowKey: "id=1", fields: [{ field: "amount", a: "10", b: "99" }] }],
  identical: false,
  basisDiffs: [
    { field: "来源修订", a: "rev-a", b: "rev-b" },
    { field: "运行批次", a: "run-1", b: "run-2" }
  ]
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => vi.unstubAllGlobals());

function stubCompareApi(savedViews: unknown[] = []) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url.startsWith("/api/projects/first-dataflow/candidates")) return jsonResponse({ candidates });
    if (url === "/api/projects/first-dataflow/compare-views" && init?.method === "POST") {
      const view = JSON.parse(String(init.body));
      savedViews.push(view);
      return jsonResponse({ views: savedViews });
    }
    if (url === "/api/projects/first-dataflow/compare-views") return jsonResponse({ views: savedViews });
    if (url === "/api/projects/first-dataflow/compare") return jsonResponse(comparison);
    return jsonResponse({}, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

test("跨运行候选对比：筛选列表、选择两个候选并排比较", async () => {
  stubCompareApi();
  render(<App />);
  await screen.findByText("跨运行候选对比");
  // 两条候选都列出，状态分别显示。
  await screen.findByText("run-1");
  expect(screen.getByText("run-2")).toBeTruthy();
  expect(screen.getAllByText("被取代").length).toBeGreaterThan(0);
  expect(screen.getAllByText("待确认").length).toBeGreaterThan(0);
  // 选择两个候选并对比。
  const checkboxes = await screen.findAllByRole("checkbox", { name: "" });
  fireEvent.click(checkboxes[0]);
  fireEvent.click(checkboxes[1]);
  fireEvent.click(screen.getByText("对比选中的两个候选"));
  await screen.findByText(/输出差异：新增 1 行 · 删除 1 行 · 字段变化 1 行/);
  expect(screen.getByText("id=1")).toBeTruthy();
  expect(screen.getByText("amount")).toBeTruthy();
  // 依据差异并排显示。
  expect(screen.getAllByText("来源修订").length).toBeGreaterThan(0);
  expect(screen.getAllByText("rev-a").length).toBeGreaterThan(0);
  expect(screen.getAllByText("rev-b").length).toBeGreaterThan(0);
});

test("输出完全相同但依据不同的候选明确显示依据差异", async () => {
  const identicalComparison = { ...comparison, added: [], removed: [], changes: [], identical: true };
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url.startsWith("/api/projects/first-dataflow/candidates")) return jsonResponse({ candidates });
    if (url === "/api/projects/first-dataflow/compare-views") return jsonResponse({ views: [] });
    if (url === "/api/projects/first-dataflow/compare") return jsonResponse(identicalComparison);
    return jsonResponse({}, 404);
  }));
  render(<App />);
  await screen.findByText("run-1");
  const checkboxes = await screen.findAllByRole("checkbox", { name: "" });
  fireEvent.click(checkboxes[0]);
  fireEvent.click(checkboxes[1]);
  fireEvent.click(screen.getByText("对比选中的两个候选"));
  await screen.findByText(/输出记录完全相同，但计算依据不同/);
  expect(screen.getByText(/仍为两条独立候选，不合并/)).toBeTruthy();
});

test("保存筛选条件为视图，重开后可继续核对", async () => {
  const savedViews: unknown[] = [];
  const fetchMock = stubCompareApi(savedViews);
  render(<App />);
  await screen.findByText("run-1");
  fireEvent.change(screen.getByPlaceholderText("视图名称"), { target: { value: "财务待确认" } });
  fireEvent.click(screen.getByText("保存筛选条件"));
  await screen.findByText(/筛选视图「财务待确认」已保存/);
  expect(savedViews.length).toBe(1);
  expect((savedViews[0] as { name: string }).name).toBe("财务待确认");
  // 保存视图后重新拉取视图列表（重开工程也能拿到同一份）。
  expect(fetchMock).toHaveBeenCalledWith("/api/projects/first-dataflow/compare-views", expect.anything());
});

