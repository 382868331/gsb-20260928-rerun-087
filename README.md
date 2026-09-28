# 数据处理流程设计网站

React 负责可视化步骤编排，Go 负责流程校验、版本持久化、执行和运行结果管理。

## 启动

1. 后端：在 backend 目录执行 go run .
2. 前端：在 frontend 目录执行 npm install && npm run dev

前端默认运行在 http://localhost:5173，Vite 将 /api 代理到 Go 服务 http://localhost:8080。

## 工程格式

保存格式为稳定的 workflow/v1 JSON：

- nodes：组件实例，包含 file-input、transform、file-output、manual。
- edges.kind=data：数据依赖，只有上游本轮成功结果会传给下游。
- edges.kind=parameter：参数依赖，把上游结果写入指定参数名，不作为行数据输入。
- version：每次保存由 Go 递增；每次运行记录对应工程版本。

后续新增处理组件时，继续向同一 nodes[].type 注册表扩展即可复用保存、连线、版本和重跑机制。

## 运行语义

- 保存时和启动前都执行 DAG 校验，循环连接不会进入运行器。
- 修改节点后，前端标记该节点及其数据/参数下游为失效。
- 新运行中失败或未产出的节点没有本轮 artifact，下游会被标记为失败，不能复用旧结果冒充本轮输出。
- 局部重跑从选中节点开始，只复用作用域外、上次成功的上游结果，并重新计算该节点及全部下游。
- 人工节点会暂停运行；批准后继续到汇合输出，拒绝则使下游失败。

## 验证

1. 后端：在 backend 目录执行 go test ./...
2. 前端：在 frontend 目录执行 npm run build

Go 测试覆盖：文件输入到输出、循环拦截、失败失效、局部重跑、人工分支后汇合。
