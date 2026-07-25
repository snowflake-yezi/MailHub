# NR-P6 DataStream 迁移验收记录

> 状态：已完成（2026-07-25）
>
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 目标与边界

NR-P6 将 P5 后仍依赖 system -> node `8081` 的只读业务迁入独立 gRPC DataStream：邮件列表和正文、raw EML、附件与预览、隔离区原件与附件。

本阶段达到远程 staging 的功能 MVP 候选范围，但不执行生产 dual canary、关闭 `8081` 或 shared secret 清理；这些仍属于 NR-P7。

## 2. 管理端实现

- `internal/nodedata` 维护每节点单活 data session，并要求 `DataStreamHello` 绑定当前 Control session ID、UUID、boot ID 和协议版本。
- 每个读取使用独立 request ID；管理端校验 header、从 1 开始的 chunk sequence、单 chunk 大小、总响应大小、声明长度、结束总字节数和 SHA-256。
- 每节点并发由 `data_max_concurrency_per_node` 限制，chunk 由 `data_chunk_size` 限制，单响应默认上限 1 GiB。
- HTTP 请求 context、业务 deadline、body close 和慢消费者都会取消对应 node 请求，不关闭其他数据请求或 ControlStream。
- `DataStreamTransport` 恢复 HTTP status、Content-Type、Content-Length、Content-Disposition、Cache-Control 和 `X-Content-Type-Options`。

## 3. 节点实现

- Agent 在 ControlStream 建立后以相同节点身份维护独立 DataStream；DataStream 自行退避重连，失败不会重启控制会话。
- 节点 dispatcher 使用管理端下发的并发与 chunk 限制执行读取，所有 gRPC Send 由单一循环串行化。
- raw EML 直接打开 Maildir 文件流式发送，不整包载入内存；附件继续复用现有 MIME 解析和预览安全规则。
- 每个请求结束时发送 SHA-256 和总字节数；取消或 deadline 会关闭正在读取的 body。
- 业务适配继续复用现有 Message-ID 索引、MIME parser、隔离区 store 和 HTTP 错误语义。

## 4. Transport 迁移语义

| 模式 | 变更命令 | 数据读取 |
|---|---|---|
| `legacy_http` | legacy HTTP | legacy HTTP |
| `dual` | ControlStream 单主通道 | DataStream 优先；请求建立失败时安全回退 legacy HTTP |
| `control_stream` | ControlStream | DataStream，不依赖可达 `api_host` |

隔离区 release status 仍读取 P5 的持久命令结果，不进入二进制 DataStream。

## 5. 验收记录

- 管理端单元测试覆盖 chunk 顺序/大小、长度、SHA-256、慢消费者、context 取消、本地 deadline 和三模式路由。
- 节点单元测试覆盖分块与校验和、阻塞 reader 取消、邮件全部读取类型和隔离区读取类型。
- gRPC bufconn 覆盖 DataStream 与 active Control session 绑定、请求/响应路由，以及数据读取取消期间 ControlStream pong 不受阻塞。
- `mgmt-system`、`mail-node`、`node-contract`、`filter-contract` 全量 Go 测试通过；新增 data/gateway/transport/handler/agent 关键包 race 测试通过。

下一阶段是 NR-P7：dual 影子对比、逐节点 canary、回滚演练、关闭 system -> node `8081` 和移除 control_stream 节点 shared secret。
