import { test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import App from "../App";

const components = [
  { type: "file_input", name: "文件输入", category: "输入", description: "d", inputs: [], outputs: [{ id: "rows", name: "数据表" }], params: [{ id: "fileName", name: "文件名", type: "string", required: true }] },
  { type: "publish", name: "发布到接收方", category: "发布", description: "d", inputs: [{ id: "rows", name: "数据表", multi: true }], outputs: [], params: [{ id: "receiver", name: "接收方", type: "string", required: true }] }
];

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => vi.unstubAllGlobals());

// 三接收方工程：pub-a → 财务，pub-b → 运营，pub-c → 人事。
const threeReceiverProject = {
  format: "dataflow.project/v1", id: "first-dataflow", name: "三接收方", revision: 3,
  nodes: [
    { id: "input-1", type: "file_input", name: "读取订单", position: { x: 0, y: 0 }, params: { fileName: "o.csv", format: "csv", content: "id\n1" } },
    { id: "pub-a", type: "publish", name: "发布财务", position: { x: 0, y: 0 }, params: { receiver: "财务" } },
    { id: "pub-b", type: "publish", name: "发布运营", position: { x: 0, y: 0 }, params: { receiver: "运营" } },
    { id: "pub-c", type: "publish", name: "发布人事", position: { x: 0, y: 0 }, params: { receiver: "人事" } }
  ],
  edges: [
    { id: "e-a", source: "input-1", target: "pub-a", sourcePort: "rows", targetPort: "rows" },
    { id: "e-b", source: "input-1", target: "pub-b", sourcePort: "rows", targetPort: "rows" },
    { id: "e-c", source: "input-1", target: "pub-c", sourcePort: "rows", targetPort: "rows" }
  ]
};

function pendingDeliveries() {
  const make = (nodeId: string, receiver: string, version: string) => ({
    nodeId, receiver,
    pending: { nodeId, receiver, version, runId: "run-1", rows: 1, empty: false, summary: "s" },
    history: [{ kind: "computed", version, runId: "run-1", note: "仅已计算" }]
  });
  return { "pub-a": make("pub-a", "财务", "va"), "pub-b": make("pub-b", "运营", "vb"), "pub-c": make("pub-c", "人事", "vc") };
}

function makeGroup(id: string, nodeIds: string[], deps: unknown[], status = "prepared") {
  const receivers: Record<string, [string, string]> = { "pub-a": ["财务", "va"], "pub-b": ["运营", "vb"], "pub-c": ["人事", "vc"] };
  return {
    id, runId: "run-1", projectVersion: "p", status, dependencies: deps,
    members: nodeIds.map((nodeId) => ({ nodeId, receiver: receivers[nodeId][0], version: receivers[nodeId][1], runId: "run-1", baseVersion: "" })),
    createdAt: "2026-10-06T00:00:00Z", updatedAt: "2026-10-06T00:00:00Z"
  };
}

// 装配 mocked 后端：行为由 behavior 回调定制。
function setupBackend(behavior: {
  createGroup?: (body: { nodeIds: string[]; dependencies: unknown[] }) => { body: unknown; status?: number };
  confirmGroup?: (groupId: string, calls: number) => { body: unknown; status?: number };
  cancelGroup?: (groupId: string) => { body: unknown; status?: number };
}) {
  const state = { groups: [] as unknown[], deliveries: pendingDeliveries(), confirmCalls: 0 };
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/components") return jsonResponse(components);
    if (url === "/api/projects" && init?.method === "POST") {
      const body = JSON.parse(String(init?.body));
      return jsonResponse({ project: { ...body, revision: (body.revision ?? 0) + 1 }, validation: { ok: true, issues: [], executionOrder: [], affectedNodeIds: [] } });
    }
    if (url === "/api/projects/first-dataflow" && !init?.method) {
      return jsonResponse({ project: threeReceiverProject, artifacts: {}, deliveries: state.deliveries, candidateArchives: [], publishGroups: state.groups });
    }
    if (url.endsWith("/run")) {
      return jsonResponse({ run: { ok: true, runId: "run-1", projectVersion: "p", affectedNodeIds: [], results: {}, deliveries: state.deliveries }, candidateArchives: [] });
    }
    if (url.endsWith("/publish-groups") && init?.method === "POST") {
      const result = behavior.createGroup!(JSON.parse(String(init?.body)));
      return jsonResponse(result.body, result.status ?? 200);
    }
    if (url.includes("/publish-groups/") && url.endsWith("/confirm")) {
      state.confirmCalls++;
      const groupId = url.split("/publish-groups/")[1].split("/")[0];
      const result = behavior.confirmGroup!(groupId, state.confirmCalls);
      return jsonResponse(result.body, result.status ?? 200);
    }
    if (url.includes("/publish-groups/") && url.endsWith("/cancel")) {
      const groupId = url.split("/publish-groups/")[1].split("/")[0];
      const result = behavior.cancelGroup!(groupId);
      return jsonResponse(result.body, result.status ?? 200);
    }
    if (url.includes("/receivers/") && url.endsWith("/availability")) return jsonResponse({ ok: true });
    return jsonResponse({}, 404);
  }));
  return state;
}

async function loadThreeReceiverPage() {
  render(<App />);
  await screen.findByText("文件输入");
  fireEvent.click(screen.getByText("重新打开已保存版本"));
  await screen.findByText("发布组（多接收方一致发布）");
  fireEvent.click(screen.getByText("整流程运行"));
  await waitFor(() => expect(screen.getAllByText(/仅已计算/).length).toBeGreaterThan(0));
}

function pickCandidates(...receivers: string[]) {
  for (const receiver of receivers) {
    fireEvent.click(screen.getByText(new RegExp("^" + receiver + " · 版本")));
  }
}

test("三接收方循环依赖：组成发布组被拒绝，整组未发布", async () => {
  setupBackend({
    createGroup: () => ({ body: { error: "接收方 财务 的依赖形成循环", publishGroups: [] }, status: 422 })
  });
  await loadThreeReceiverPage();
  pickCandidates("财务", "运营", "人事");
  // 声明 财务→运营→人事→财务 的循环依赖。
  for (let index = 0; index < 3; index++) fireEvent.click(screen.getByText("添加依赖"));
  const pairs: [string, string][] = [["财务", "运营"], ["运营", "人事"], ["人事", "财务"]];
  pairs.forEach(([from, on], index) => {
    fireEvent.change(screen.getByLabelText("依赖方-" + index), { target: { value: from } });
    fireEvent.change(screen.getByLabelText("被依赖方-" + index), { target: { value: on } });
    fireEvent.change(screen.getByLabelText("依赖版本-" + index), { target: { value: "v" + index } });
  });
  fireEvent.click(screen.getByText(/组成发布组（3 个候选）/));
  await waitFor(() => expect(screen.getByText(/发布组未发布：.*循环/)).toBeTruthy());
  expect(screen.queryByText(/确认发布整组/)).toBeNull();
  // 历史候选对比仍保持只读：没有任何确认 / 发布操作。
  const comparison = screen.getByText("跨运行候选对比（只读）").closest("section")!;
  expect(within(comparison as HTMLElement).queryByRole("button", { name: /确认|发布/ })).toBeNull();
});

test("两组交叉争用同一接收方：可见版本偏离共同前提即整组中止", async () => {
  const group1 = makeGroup("pg-1", ["pub-a", "pub-b"], []);
  setupBackend({
    createGroup: () => ({ body: { group: group1, publishGroups: [group1] } }),
    confirmGroup: () => ({
      status: 409,
      body: {
        error: "接收方 财务 的可见版本已变为 v-other（建组前提为 （无）），与其它发布组冲突，整组保持未发布",
        group: { ...group1, status: "aborted", statusNote: "与其它发布组冲突" },
        publishGroups: [{ ...group1, status: "aborted", statusNote: "与其它发布组冲突" }],
        deliveries: pendingDeliveries()
      }
    })
  });
  await loadThreeReceiverPage();
  pickCandidates("财务", "运营");
  fireEvent.click(screen.getByText(/组成发布组（2 个候选）/));
  await screen.findByText("确认发布整组");
  fireEvent.click(screen.getByText("确认发布整组"));
  await waitFor(() => expect(screen.getByText(/与其它发布组冲突，整组保持未发布/)).toBeTruthy());
  expect(screen.getByText("已中止（整组未发布）")).toBeTruthy();
  // 整组保持未发布：接收方仍只看到旧的待确认候选。
  expect(screen.queryByText("已对接收方可见")).toBeNull();
});

test("提交期间接收方断开：组停留提交中，恢复后到达确定结局且不重复发布", async () => {
  const group1 = makeGroup("pg-1", ["pub-a", "pub-b", "pub-c"], []);
  const committing = {
    ...group1, status: "committing",
    statusNote: "接收方 运营 暂时不可用，组停留在提交中；恢复后重试即可继续",
    outcomes: [
      { receiver: "财务", nodeId: "pub-a", version: "va", runId: "run-1", committed: true },
      { receiver: "人事", nodeId: "pub-c", version: "vc", runId: "run-1", committed: true }
    ]
  };
  const committedDeliveries = () => {
    const deliveries = pendingDeliveries();
    for (const nodeId of ["pub-a", "pub-b", "pub-c"] as const) {
      deliveries[nodeId] = { ...deliveries[nodeId], published: deliveries[nodeId].pending, pending: undefined } as never;
    }
    return deliveries;
  };
  setupBackend({
    createGroup: () => ({ body: { group: group1, publishGroups: [group1] } }),
    confirmGroup: (_groupId, calls) => calls === 1
      ? { status: 409, body: { error: "接收方 运营 暂时不可用，发布组仍在提交中", group: committing, publishGroups: [committing], deliveries: pendingDeliveries() } }
      : {
          body: {
            group: { ...group1, status: "committed", statusNote: "全部接收方已采用组内候选", outcomes: [...committing.outcomes, { receiver: "运营", nodeId: "pub-b", version: "vb", runId: "run-1", committed: true }] },
            publishGroups: [{ ...group1, status: "committed", outcomes: [...committing.outcomes, { receiver: "运营", nodeId: "pub-b", version: "vb", runId: "run-1", committed: true }] }],
            deliveries: committedDeliveries()
          }
        }
  });
  await loadThreeReceiverPage();
  pickCandidates("财务", "运营", "人事");
  // 模拟 运营 接收方暂时不可用。
  fireEvent.click(screen.getByText("模拟 运营 暂时不可用"));
  fireEvent.click(screen.getByText(/组成发布组（3 个候选）/));
  await screen.findByText("确认发布整组");
  fireEvent.click(screen.getByText("确认发布整组"));
  await waitFor(() => expect(screen.getByText(/提交中（需恢复到确定结局）/)).toBeTruthy());
  expect(screen.getByText(/组停留在提交中/)).toBeTruthy();
  // 提交中不能取消，只能恢复。
  expect(screen.queryByText("取消发布组")).toBeNull();
  // 恢复可用后重试：整组完成，每个接收方实际采用的候选身份留痕。
  fireEvent.click(screen.getByText("模拟 运营 暂时不可用"));
  fireEvent.click(screen.getByText("恢复提交"));
  await waitFor(() => expect(screen.getByText("已完成（整组对接收方可见）")).toBeTruthy());
  expect(screen.getByText((_text, element) => element?.tagName === "P" && !!element.textContent?.includes("实际采用：财务 ← va；人事 ← vc；运营 ← vb"))).toBeTruthy();
  expect(screen.getAllByText("已对接收方可见").length).toBe(3);
});

test("过期候选混入：共同校验拒绝，整组保持未发布；准备阶段可取消", async () => {
  const group1 = makeGroup("pg-1", ["pub-a", "pub-b"], []);
  const group2 = makeGroup("pg-2", ["pub-a", "pub-b"], []);
  let created = 0;
  setupBackend({
    createGroup: () => {
      created++;
      const group = created === 1 ? group1 : group2;
      return { body: { group, publishGroups: created === 1 ? [group1] : [{ ...group1, status: "aborted", statusNote: "混入失效候选" }, group2] } };
    },
    confirmGroup: () => ({
      status: 409,
      body: {
        error: "接收方 运营 的候选已失效：来源运行失败，候选失去运行依据，整组保持未发布",
        group: { ...group1, status: "aborted", statusNote: "混入失效候选" },
        publishGroups: [{ ...group1, status: "aborted", statusNote: "混入失效候选" }, group2],
        deliveries: pendingDeliveries()
      }
    }),
    cancelGroup: () => ({ body: { group: { ...group2, status: "cancelled", statusNote: "用户在提交前取消" }, publishGroups: [{ ...group1, status: "aborted" }, { ...group2, status: "cancelled", statusNote: "用户在提交前取消" }] } })
  });
  await loadThreeReceiverPage();
  pickCandidates("财务", "运营");
  fireEvent.click(screen.getByText(/组成发布组（2 个候选）/));
  await screen.findByText("确认发布整组");
  // 混入过期候选后确认：整组中止，任何接收方都未发布。
  fireEvent.click(screen.getByText("确认发布整组"));
  await waitFor(() => expect(screen.getByText(/候选已失效.*整组保持未发布/)).toBeTruthy());
  expect(screen.getByText("已中止（整组未发布）")).toBeTruthy();
  expect(screen.queryByText("已对接收方可见")).toBeNull();
  // 再组成一组并在提交前取消。
  fireEvent.click(screen.getByText(/组成发布组（2 个候选）/));
  await screen.findByText("取消发布组");
  fireEvent.click(screen.getByText("取消发布组"));
  await waitFor(() => expect(screen.getByText("已取消（整组未发布）")).toBeTruthy());
});
