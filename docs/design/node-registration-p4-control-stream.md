# NR-P4 ControlStream、会话与 lease

> 状态：已完成（2026-07-25）
>
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 目标与边界

NR-P4 建立由 mail-node 主动发起的 TLS gRPC ControlStream，使管理端能够用每节点凭证识别连接、维护单活 session 和 lease，并通过既有长连接下发 revision 通知。

本阶段是控制面技术闭环，不是远程业务 MVP：

- 不投递或执行邮箱、域名、生命周期和隔离区业务命令；这些属于 NR-P5。
- 不实现正文、raw EML、附件和隔离区原件读取；这些属于 NR-P6 DataStream。
- 不关闭 node `8081`，不移除 `api_host` 或 shared secret。
- 不自动启用新节点 allocation；新注册节点继续保持 `disabled`。

## 2. 管理端实现

- `internal/nodesession` 保存单实例内的 `server_id -> active session` 映射。新 session 原子接管并取消旧 session；旧连接退出时不能覆盖新连接状态。
- `internal/nodegateway` 实现 Control RPC，认证 `Authorization: Node <credential>` 和 `x-mailhub-node-uuid`，凭证映射出的 server/UUID 是唯一可信身份。
- Hello 协商协议版本并返回 Welcome；没有共同版本、UUID 不一致、revision 超前和握手前非 Hello 帧均被拒绝。
- 每次有效 Heartbeat 刷新 lease、connection、readiness、load、boot ID 和 applied revision。流断开或 lease 过期立即停止 control/dual 节点的新分配，不改变 enrollment 与 allocation 管理意图。
- 单独 TLS listener 要求证书和私钥，最低 TLS 1.2；凭证撤销后立即取消当前 session。
- `ControlStreamTransport` 在已连接时发送 config/filter revision 通知；NR-P5/NR-P6 尚未迁移的调用继续使用 `LegacyHTTPTransport`。

## 3. mail-node Agent

- `internal/agent` 从受保护身份目录读取 UUID 和节点 credential，仅在 `dual` 或 `control_stream` 模式启动。
- Agent 使用 TLS 主动连接 management `control_url`，发送 Hello，按 Welcome 间隔立即并周期发送 Heartbeat。
- 断线后采用有上限的指数退避和 jitter 重连；收到 config/filter 通知后唤醒现有 HTTPS 全量拉取，通知丢失仍由周期拉取收敛。
- 配置与策略 HTTPS 客户端优先使用节点 credential；shared secret 仅保留迁移兼容路径。
- Heartbeat 上报邮箱数、磁盘容量、config 错误和 Maildir/Postfix/Dovecot/OpenDKIM 本地检查摘要。
- 纯 `control_stream` 模式停止旧 HTTP 心跳和 server ID 自动发现；`dual` 模式保留 legacy 业务链路用于 canary。

## 4. 状态与回滚

- `legacy_http` 分配继续依赖旧健康探测。
- `control_stream` 分配要求 connected、未过期 lease、ready、allocation active。
- `dual` 同时要求 legacy healthy 与有效 control lease，任一链路失败都停止新分配。
- 回退到 `legacy_http` 不回滚 UUID、凭证、审批、desired/applied revision 或已经记录的审计事实。

## 5. 验收记录

- gRPC bufconn 覆盖节点凭证 metadata、Hello/Welcome、错误凭证、无共同协议、立即心跳、revision 通知、Ping/Pong 和断线落库。
- 本地自签 CA/TLS listener 完成真实 TLS ControlStream 握手。
- Agent 集成测试先注入一次拨号失败，再验证重连、握手、心跳、config/filter 唤醒和 ConfigApplied。
- session/store 测试覆盖单活接管、旧 session 条件删除、凭证撤销、lease 过期和 allocation/enrollment 不被网络事件改写。
- `mgmt-system`、`mail-node`、`node-contract`、`filter-contract` 全量 `go test ./...`、`go vet ./...` 和 `go mod verify` 通过。
- Gateway/session/transport/store/handler/service 与 Agent/config/filterpolicy 的 race 测试通过。
- Web 1014 个三语键、UI contract 和 Vite production build 通过；仅保留既有的 500 kB chunk warning。
- 未部署、未切换生产 transport、未启用新节点 allocation，也未改变广告过滤 `dual_shadow/false` 安全边界。

