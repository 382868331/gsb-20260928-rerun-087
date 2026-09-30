import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import App from "./App";
import type { ComponentDef } from "./types";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const components: ComponentDef[] = [
  { type: "file_input", name: "文件输入", category: "输入", description: "d", outputs: [{ id: "rows", name: "数据表" }], inputs: [], params: [{ id: "fileName", name: "文件名", type: "string", required: true }] },
  { type: "transform", name: "转换", category: "处理", description: "d", inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [{ id: "rows", name: "数据表" }], params: [{ id: "operation", name: "操作", type: "select", required: true, options: ["passthrough"] }] },
  { type: "file_output", name: "文件输出", category: "输出", description: "d", inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [], params: [{ id: "path", name: "路径", type: "string", required: true }] }
];

const savedProject = {
  format: "dataflow.project/v1", id: "first-dataflow", name: "自带样例：分支与汇合", revision: 1,
  nodes: [
    { id: "input-1", type: "file_input", name: "读取订单 CSV", position: { x: 0, y: 0 }, params: { fileName: "orders.csv", format: "csv", content: "id,city\n1001,Shanghai,88" } },
    { id: "filter-1", type: "transform", name: "只保留上海订单", position: { x: 0, y: 0 }, params: { operation: "filter_equals", field: "city", value: "Shanghai" } },
    { id: "output-1", type: "file_output", name: "写出上海订单", position: { x: 0, y: 0 }, params: { path: "out.json", format: "json" } }
  ],
  edges: [
    { id: "e1", source: "input-1", target: "filter-1", sourcePort: "rows", targetPort: "rows" },
    { id: "e2", source: "filter-1", target: "output-1", sourcePort: "rows", targetPort: "rows" }
  ]
};

const successfulRun = {
  ok: true, runId: "run-A", projectVersion: "v-saved", affectedNodeIds: ["input-1", "filter-1", "output-1"],
  results: {
    "input-1": { nodeId: "input-1", status: "succeeded", basis: "本轮新执行", artifact: { nodeId: "input-1", version: "av", runId: "run-A", projectVersion: "v-saved", rows: [{ id: "1001", city: "Shanghai" }], summary: "读取 1 行" } },
    "filter-1": { nodeId: "filter-1", status: "succeeded", basis: "本轮新执行", artifact: { nodeId: "filter-1", version: "fv", runId: "run-A", projectVersion: "v-saved", rows: [{ id: "1001", city: "Shanghai" }], summary: "过滤后 1 行" } },
    "output-1": { nodeId: "output-1", status: "succeeded", basis: "本轮新执行", artifact: { nodeId: "output-1", version: "ov", runId: "run-A", projectVersion: "v-saved", path: "out.json", rows: [{ id: "1001", city: "Shanghai" }], summary: "写出 1 行" } }
  }
};

function mockFetch(handler: (url: string, init?: RequestInit) => unknown) {
  globalThis.fetch = vi.fn(async (url: string, init?: RequestInit) => ({ ok: true, json: async () => handler(url, init) })) as unknown as typeof fetch;
}

beforeEach(() => {
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
  globalThis.structuredClone = ((value: unknown) => JSON.parse(JSON.stringify(value))) as typeof structuredClone;
  mockFetch(() => components);
});

afterEach(() => vi.restoreAllMocks());
afterEach(() => cleanup());

describe("first-load states", () => {
  it("shows the bundled sample on first load", async () => {
    render(<App />);
    expect(await screen.findByText("读取订单 CSV")).toBeTruthy();
    expect(screen.getByText("分支汇合")).toBeTruthy();
  });

  it("blank project renders and can still be edited", async () => {
    render(<App />);
    await screen.findByText("读取订单 CSV");
    fireEvent.click(screen.getByText("空白工程"));
    expect(screen.queryByText("读取订单 CSV")).toBeNull();
    expect(screen.getByText("空白工程")).toBeTruthy();
  });

  it("reloads the sample after blanking", async () => {
    render(<App />);
    await screen.findByText("读取订单 CSV");
    fireEvent.click(screen.getByText("空白工程"));
    fireEvent.click(screen.getByText("载入样例"));
    expect(await screen.findByText("读取订单 CSV")).toBeTruthy();
  });
});

describe("run semantics in the UI", () => {
  it("marks downstream steps stale after changing a parameter and never shows the old artifact as this-round success", async () => {
    const runClone = JSON.parse(JSON.stringify(successfulRun));
    mockFetch((url, init) => {
      if (url === "/api/components") return components;
      if (url === "/api/projects" && init?.method === "POST") return { project: savedProject, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } };
      if (url.endsWith("/run") && init?.method === "POST") {
        const failed = JSON.parse(JSON.stringify(runClone));
        failed.ok = false;
        failed.results["filter-1"] = { nodeId: "filter-1", status: "failed", error: "文件输入失败：模拟错误", basis: "本轮新执行" };
        failed.results["output-1"] = { nodeId: "output-1", status: "blocked", error: "上游步骤 filter-1 失败或被阻断", basis: "阻断链路", artifact: undefined };
        delete failed.results["filter-1"].artifact;
        return failed;
      }
      return {};
    });
    render(<App />);
    fireEvent.click(await screen.findByText("整流程运行"));
    await waitFor(() => expect(screen.getByText(/运行失败/)).toBeTruthy());
    fireEvent.click(screen.getByText("写出上海订单"));
    expect(screen.getAllByText(/上游步骤/).length).toBeGreaterThan(0);
    expect(screen.queryByText("out.json")).toBeNull();
  });

  it("shows which steps were recomputed and which were reused with the reason", async () => {
    const reuseRun = JSON.parse(JSON.stringify(successfulRun));
    reuseRun.affectedNodeIds = ["input-2"];
    reuseRun.results["input-1"] = { nodeId: "input-1", status: "reused", reusedFrom: "run-A", basis: "沿用 run-A 的产物：节点版本 av 一致", artifact: reuseRun.results["input-1"].artifact };
    reuseRun.results["filter-1"] = { nodeId: "filter-1", status: "reused", reusedFrom: "run-A", basis: "沿用 run-A 的产物：节点版本 fv 一致", artifact: reuseRun.results["filter-1"].artifact };
    mockFetch((url, init) => {
      if (url === "/api/components") return components;
      if (url === "/api/projects" && init?.method === "POST") return { project: savedProject, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } };
      if (url.endsWith("/run") && init?.method === "POST") return reuseRun;
      return {};
    });
    render(<App />);
    fireEvent.click(await screen.findByText("整流程运行"));
    expect(await screen.findByText(/沿用上一轮产物（2）/)).toBeTruthy();
    expect(screen.getByText(/本轮重算（1）/)).toBeTruthy();
    expect(screen.getByText(/节点版本 av 一致/)).toBeTruthy();
  });

  it("reopen aligns the page with the actual run version and flags stale artifacts", async () => {
    mockFetch((url) => {
      if (url === "/api/components") return components;
      if (url === "/api/projects/first-dataflow") return { project: savedProject, artifacts: {}, projectVersion: "v-changed" };
      if (url.endsWith("/runs/latest")) return { exists: true, run: { ...successfulRun, projectVersion: "v-old" } };
      return {};
    });
    render(<App />);
    await screen.findByText("读取订单 CSV");
    fireEvent.click(screen.getByText("重新打开"));
    expect(await screen.findByText(/最后一次运行基于旧版本/)).toBeTruthy();
    expect(screen.getByText(/页面工程版本与最后一次运行版本不一致/)).toBeTruthy();
  });
});
