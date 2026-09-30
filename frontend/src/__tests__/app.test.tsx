import { test, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import App from "../App";

test("first load renders without white screen", async () => {
  render(<App />);
  expect(await screen.findByText("数据处理流程设计器")).toBeTruthy();
});
