import { test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import App from "../App";

test("first load renders without white screen", async () => {
  render(<App />);
  expect(await screen.findByText("数据处理流程设计器")).toBeTruthy();
});

test("null port lists in component contract do not crash the canvas", async () => {
  const component = {
    type: "file_input", name: "文件输入", category: "输入", description: "d",
    inputs: null, outputs: null, params: []
  };
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify([component]), { status: 200, headers: { "Content-Type": "application/json" } })));
  render(<App />);
  const addButton = await screen.findByText("文件输入");
  fireEvent.click(addButton);
  expect(screen.getByRole("heading", { name: "数据处理流程设计器" })).toBeTruthy();
  vi.unstubAllGlobals();
});
