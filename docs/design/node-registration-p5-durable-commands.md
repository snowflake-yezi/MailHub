# NR-P5 持久化命令迁移验收记录

> 状态：已完成（2026-07-25）
>
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 目标与边界

NR-P5 将变更类 system -> node 调用迁入 ControlStream，并以“管理端持久化命令 + 至少一次投递 + 节点持久幂等结果”实现业务上的等效一次。

本阶段不迁移邮件正文、raw EML、附件、预览和隔离区原件读取；这些仍属于 NR-P6 DataStream。P5 完成不代表可以关闭 node `8081`，也不代表远程业务 MVP 已完成。

## 2. 管理端实现

- `internal/store/node_command_store.go` 实现节点内单调 sequence、节点范围幂等键、deadline、条件状态转换、重复结果校验和失败/过期 retry sequence。
- `internal/nodecommand` 保证命令先提交 `node_commands` 再进入 active session 队列；新 session 建立后按 sequence 重放全部未终态命令。
- Gateway 接受 `CommandReceived`、`CommandStarted` 和 `CommandResult`，持久化 `queued -> delivered -> received -> running -> terminal` 状态。
- `ControlStreamTransport.Execute` 在短窗口内等待最终结果，并把节点结果还原为兼容的 HTTP 状态、响应头和 JSON body。
- 等待超时不回滚命令；同步 API 返回 `202`、`operation_id` 和 pending 状态。命令使用独立 24 小时 deadline，避免把短同步等待误当成离线队列寿命。
- `dual` 和 `control_stream` 的已迁移变更命令以 ControlStream 为单一主通道，不在失败后回落 legacy 形成双写；只读数据继续走 legacy，等待 P6。
- 隔离区 release status 从持久命令结果恢复，不要求 system 反向访问 node。

## 3. 节点实现

- `internal/command` 在受保护 identity 目录维护 `command-journal.json`；收到命令先原子落盘，再发送 `received`。
- dispatcher 在执行前持久化 `running`，业务结果先持久化再发送 `CommandResult`。重复 command ID 直接返回缓存结果，不重复执行业务副作用。
- journal 不保存明文 payload，只保存 payload 指纹、命令身份和必要结果；默认保留 30 天、最多 2048 条终态记录，文件权限为 `0600`。
- Agent Hello/Heartbeat 上报最近完成 sequence；命令执行队列独立于 ControlStream 收发循环，长命令不阻塞心跳。
- 命令适配器复用现有 `NodeHandler` 和 application manager，没有在 gRPC handler 重写 Postfix、Dovecot、Maildir、DKIM 或隔离区业务。

## 4. 已迁移命令

| 范围 | 命令 |
|---|---|
| revision | `config.revision.changed.v1`、`filter.revision.changed.v1` 通知与周期对账 |
| 域名 | `domain.apply.v1`、`domain.remove.v1`、`domain.inspect.v1` |
| 邮箱 | `mailbox.create.v1`、`mailbox.password.v1`、`mailbox.delete.v1`、`mailbox.restore.v1` |
| 消息与生命周期 | `message.delete.v1`、`message.retention.purge.v1`、`quarantine.gc.v1` |
| 隔离区 | `quarantine.release.v1` 和基于持久结果的 release status |

域名 Apply 的 Postfix 成功、DKIM 失败会记录 `succeeded_with_warning`，原有管理端状态继续映射为 `partial` 并保留 selector、公钥、错误和 DNS 清单。

## 5. 故障恢复语义

- system 在发送帧前退出：命令已在数据库中，重启后随新 session 重投。
- node 在 `received` 或 `running` 后退出：system 保持未终态命令，node 重连后用 journal 缓存结果或重新执行幂等业务。
- 结果帧丢失：system 重投相同 command ID，node 从 journal 返回相同终态结果。
- 同步调用超时：调用方获得 operation ID；命令继续执行，后续重试或状态查询读取持久结果。
- terminal failure、reject 或 deadline expired：下一次业务重试创建新的 retry sequence，已完成事实不被覆盖。

## 6. 验收记录

- store 测试覆盖 sequence、payload 冲突、合法状态转换、重复/冲突结果、deadline 和 retry sequence。
- ControlStream transport 测试覆盖先持久化再投递、同步等待、离线 queued、management 重启后重投和隔离区持久 status。
- node journal/dispatcher 测试覆盖结果落盘、进程重启、running 恢复、幂等键冲突、重复投递不重复执行。
- 节点业务适配测试使用临时 Postfix/Dovecot 文件验证 mailbox create 重复执行不重复写配置，并验证域名 partial warning。
- gRPC bufconn 测试验证 Agent 返回 received/started/result，强制断线重连后 Hello 上报已完成 sequence，executor 只执行一次。
- `mgmt-system`、`mail-node`、`node-contract`、`filter-contract` 全量 `go test ./...`、`go vet ./...` 和 `go mod verify` 通过。
- 管理端 store/gateway/session/transport/handler/service/lifecycle 与节点 command/agent/handler/mailbox/domain/quarantine/forward race 测试通过。
- Web 1014 个三语键、UI contract 和 Vite production build 通过；仅保留既有的 500 kB chunk warning。
- 未部署、未切换生产 transport、未关闭 node `8081`，也未改变广告过滤 `dual_shadow/false` 安全边界。

下一阶段是 NR-P6 DataStream：邮件列表和正文、raw EML、附件/预览、隔离区原件/附件。
