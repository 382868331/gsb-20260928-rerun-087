import { test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import App from "../App";

const components = [
  {
    type: "file_input", name: "文件输入", category: "输入", description: "d",
    inputs: null as null | any[], outputs: [{ id: "rows", name: "数据表" }],
    params: [
      { id: "fileName", name: "文件名", type: "string", required: true },
      { id: "format", name: "格式", type: "select", required: true, options: ["csv", "json"] },
      { id: "content", name: "内容", type: "text", required: true }
    ]
  },
  {
    type: "transform", name: "转换", category: "处理", description: "d",
    inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [{ id: "rows", name: "数据表" }],
    params: [
      { id: "operation", name: "操作", type: "select", required: true, options: ["passthrough", "filter_equals"] },
      { id: "field", name: "字段", type: "string" },
      { id: "value", name: "值", type: "string" }
    ]
  },
  {
    type: "file_output", name: "文件输出", category: "输出", description: "d",
    inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [] as any[],
    params: [
      { id: "path", name: "路径", type: "string", required: true },
      { id: "format", name: "格式", type: "select", required: true, options: ["json", "csv"] }
    ]
  }
];

function jsonResponse(value: unknown, status = 200) {
  return { ok: status < 400 || status === 422, status, json: async () => value } as Response;
}

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async (url: string | URL | Request) => {
    if (String(url) === "/api/components") return jsonResponse(components);
    return jsonResponse({ error: "not found" }, 404);
  }));
});

afterEach(() => { vi.unstubAllGlobals(); cleanup(); });

test("first load renders without white screen even when component ports serialize as null", async () => {
  render(<App />);
  expect(await screen.findByRole("heading", { name: "数据处理流程设计器" })).toBeTruthy();
  expect(screen.getByText("读取订单 CSV")).toBeTruthy();
  fireEvent.click(await screen.findByRole("button", { name: /文件输入/ }));
  expect(screen.getByRole("heading", { name: "数据处理流程设计器" })).toBeTruthy();
});

test("blank project enters editor and unpublished revisions can be discarded", async () => {
  render(<App />);
  await screen.findByRole("heading", { name: "数据处理流程设计器" });
  fireEvent.click(screen.getByRole("button", { name: "新建空工程" }));
  expect(screen.getByText(/已新建空工程/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "整流程运行" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "撤回未发布修订" }).hasAttribute("disabled")).toBe(true);

  const savedProject = {
    format: "dataflow.project/v1", id: "blank-dataflow", name: "空工程", revision: 1,
    nodes: [], edges: []
  };
  vi.mocked(fetch).mockImplementation(async (url: string | URL | Request, init?: RequestInit) => {
    if (String(url) === "/api/components") return jsonResponse(components);
    if (String(url) === "/api/projects" && init?.method === "POST") {
      return jsonResponse({ project: savedProject, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } });
    }
    if (String(url) === "/api/projects/blank-dataflow") return jsonResponse({ project: savedProject, artifacts: {} });
    return jsonResponse({ error: "x" }, 404);
  });

  fireEvent.click(screen.getByRole("button", { name: "保存版本" }));
  await waitFor(() => expect(screen.getByText(/已保存为 revision 1/)).toBeTruthy());
  expect(screen.getByRole("button", { name: "撤回未发布修订" }).hasAttribute("disabled")).toBe(true);

  fireEvent.click(await screen.findByRole("button", { name: /文件输入/ }));
  await waitFor(() => expect(screen.getByRole("button", { name: "撤回未发布修订" }).hasAttribute("disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "撤回未发布修订" }));
  await waitFor(() => expect(screen.getByText(/已撤回未发布的修订/)).toBeTruthy());
  expect(screen.getByRole("button", { name: "撤回未发布修订" }).hasAttribute("disabled")).toBe(true);
});

test("reopen fetches the saved revision from the server", async () => {
  const savedProject = {
    format: "dataflow.project/v1", id: "first-dataflow", name: "保存版", revision: 3,
    nodes: [{ id: "n1", type: "file_input", name: "服务端名称", position: { x: 1, y: 2 }, params: { fileName: "a.csv", format: "csv", content: "id\\n1" } }],
    edges: []
  };
  vi.mocked(fetch).mockImplementation(async (url: string | URL | Request) => {
    if (String(url) === "/api/components") return jsonResponse(components);
    if (String(url) === "/api/projects/first-dataflow") return jsonResponse({ project: savedProject, artifacts: {} });
    return jsonResponse({ error: "x" }, 404);
  });
  render(<App />);
  await screen.findByRole("heading", { name: "数据处理流程设计器" });
  fireEvent.click(screen.getByRole("button", { name: "重新打开已保存版本" }));
  await waitFor(() => expect(screen.getByText(/已重新打开保存的 revision 3/)).toBeTruthy());
  expect(screen.getByText("服务端名称")).toBeTruthy();
});
