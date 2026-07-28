# 节点注册发现与出站控制通道实施计划

> 状态：NR-P0 至 NR-P6 已完成；NR-P7 代码完成，下一步远程 canary、回滚和防火墙验收
>
> 优先级：P0，当前开发主线
>
> 日期：2026-07-25
>
> 上位设计：[Mail-node 注册、身份与出站控制通道设计](node-enrollment-control-channel-design.md)
>
> 运维契约：[Mail-node 节点注册与加入集群指南](../node-registration-guide.md)

---

## 1. 本轮目标

本轮完成 `mgmt-system <-> mail-node` 协同架构改造，最终使正常运行只要求 node 主动访问 system，不再要求 system 访问 node 的 `8081`。

```text
当前
  api_host 作为身份
  + 全局 shared_secret
  + system -> node:8081 HTTP
  + node -> system HTTP

本轮目标
  node_uuid 永久身份
  + 一次性 Enrollment Token
  + 每节点独立运行凭证
  + node -> system 长期 ControlStream
  + node -> system 长期 DataStream
  + node -> system HTTPS 拉取/上报
  + 持久化命令与幂等结果
```

本轮只替换节点身份、注册、鉴权、通信和可靠投递层。Postfix、Dovecot、OpenDKIM、Maildir、邮箱、域名、过滤、转发和生命周期的现有业务实现继续作为事实源。

---

## 2. 主线优先级

从本计划批准起：

1. 节点注册发现与出站控制通道是唯一 P0 架构主线。
2. 广告过滤重构暂停在当前状态，不继续 S11/S12、策略调优、生产 `dual_filter` 或自动隔离。
3. 不回滚现有广告过滤代码、表结构和 `dual_shadow` 运行状态。
4. 只有为保证节点通信迁移兼容而必须调整过滤通知、bundle 拉取或状态上报时，才允许修改过滤相关代码。
5. 节点注册主线验收完成前，不并行引入 MinIO、Maildir 复制、控制面 HA 或新的外部 API 大改。

---

## 3. 已确认决策

| ID | 决策 | 本轮口径 |
|---|---|---|
| NR-D01 | 网络方向 | 所有控制连接均由 node 主动发起；迁移完成后 system 不访问 node `8081` |
| NR-D02 | 永久身份 | node 本地生成并保存 UUID；IP、主机名、端口不是身份 |
| NR-D03 | 注册 | system 创建短期邀请，node 申请，system 审批；支持可选预绑定 UUID |
| NR-D04 | 运行凭证 | 本轮使用 TLS + 每节点独立 Token；完整值仅签发时返回，system 只保存哈希 |
| NR-D05 | 最终认证 | mTLS 保留为后续加固，不作为本轮关闭 `8081` 的前置条件 |
| NR-D06 | 实时通道 | 使用 gRPC 双向流，不使用 Nacos、Consul 或消息中间件替代控制面 |
| NR-D07 | 流量隔离 | ControlStream 与 DataStream 分离；附件和 raw EML 不进入控制流 |
| NR-D08 | 配置与策略 | 长连接只通知 revision；完整配置和 bundle 继续由 node 通过 HTTPS 拉取 |
| NR-D09 | 可靠命令 | system 先持久化命令再投递；至少一次投递，node 幂等执行 |
| NR-D10 | API 语义 | 本轮保留现有同步外部 API；超时返回 operation/request ID，不全面改为 202 异步 API |
| NR-D11 | 节点调度 | 分拆 enrollment、connection、readiness、allocation；不再只依赖 `healthy` |
| NR-D12 | 域名结果 | 保留 Postfix 成功、DKIM 失败的 `partial` 语义，禁止粗暴整体回滚 |
| NR-D13 | 地址模型 | 邮件公开地址与控制地址解耦；DNS 不再依赖 `api_host` 推导 |
| NR-D14 | 敏感 payload | 本轮不做命令 payload 应用层加密或 Secret Manager 集成 |
| NR-D15 | 管理入口 | 不开发 node UI；注册、审批、状态和审计全部进入 system UI |
| NR-D16 | 广告过滤 | 暂停功能开发，仅保证通信迁移后现有过滤行为不回归 |

NR-D14 不表示允许密码进入日志。TLS、凭证哈希、root-only 文件、日志脱敏和响应脱敏仍是最低要求。

---

## 4. 不在本轮范围

- Maildir 跨节点复制、共享存储和故障接管。
- SMTP、IMAP、Postfix、Dovecot、OpenDKIM 核心逻辑重写。
- 自动操作 DNS 服务商。
- 命令 payload 字段级加密、信封加密或外部 Secret Manager。
- mTLS 客户端证书签发和内部 CA 生命周期。
- Kafka、NATS、RabbitMQ、Consul、etcd 或 Nacos。
- `mgmt-system` 多实例会话路由。
- 外部 API 全面异步化。
- node 本地管理 UI。
- 广告 detector、权重、阈值、样本回放和自动隔离继续开发。
- MinIO 或附件对象存储。

---

## 5. 目标交互边界

### 5.1 物理连接与逻辑方向

```text
物理连接：mail-node --------------------> mgmt-system
逻辑消息：mail-node <-------------------> mgmt-system
```

node 主动建立连接后，system 可以在已建立的双向流中发送消息。这不等于 system 能主动拨号访问 node。

### 5.2 三条运行链路

```text
ControlStream（长期 gRPC 双向流）
  小消息、强时效：握手、心跳、命令、ACK、结果、revision 通知

DataStream（独立长期 gRPC 双向流）
  数据读取：邮件正文、raw EML、附件、预览、隔离区原件

Node HTTPS Client（普通出站 HTTPS）
  完整配置、过滤 bundle、快照、决策事件、删除任务补偿
```

ControlStream 与 DataStream 必须使用独立的发送队列和并发限制。大附件不得占用 ControlStream 的发送锁、队列额度或心跳窗口。

### 5.3 组件所有权

| 组件 | 所有者 | 说明 |
|---|---|---|
| 节点台账、邀请、凭证、命令、审计 | mgmt-system | 控制面事实源 |
| 邮箱、订单、域名绑定、期望配置 | mgmt-system | 业务期望状态 |
| Maildir、Postfix、Dovecot、DKIM 文件 | mail-node | 节点实际状态 |
| 命令传输 | NodeTransport | 不能散落拼接 node URL |
| 命令执行 | mail-node application services | 复用现有 manager，不在 gRPC handler 重写业务 |
| 邮件二进制读取 | DataStream | 不落命令表，不整包缓存在内存 |

---

## 6. 当前代码改造地图

### 6.1 mgmt-system 当前直接访问 node 的位置

以下调用必须先收口到 `NodeTransport`：

| 当前模块 | 当前行为 | 目标方法 |
|---|---|---|
| `internal/healthcheck/scheduler.go` | `GET /internal/health` | 会话 lease + node 自检事件 |
| `internal/service/mailbox_creator.go` | 创建邮箱 | `Execute(mailbox.create.v1)` |
| `internal/handler/mailbox.go` | 删除、恢复、改密 | `Execute(mailbox.*.v1)` |
| `internal/handler/server.go` | 域名添加、删除 | `Execute(domain.*.v1)` |
| `internal/handler/email.go` | 列表、正文、raw、附件 | `Query/OpenData` |
| `internal/handler/filter_policy.go` | 隔离区读取和放行 | `Execute/Query/OpenData` |
| `internal/lifecycle/scheduler.go` | 删除重试、保留期、隔离清理 | 持久化维护命令 |
| `internal/handler/config.go` | 配置重载 | `Notify(config.revision.v1)` |
| `internal/handler/filter.go` | 过滤重载 | `Notify(filter.revision.v1)` |
| `internal/handler/integrated_mailbox.go` | 转发配置重载 | `Notify(config.revision.v1)` |
| `internal/handler/util.go` | JSON/二进制 HTTP 代理 | transport JSON/DataStream 适配器 |

任何新业务代码不得继续使用 `"http://" + srv.APIHost + "/internal/..."`。

### 6.2 mail-node 继续保留的业务组件

| 组件 | 本轮原则 |
|---|---|
| `internal/mailbox.Manager` | 保留实际邮箱文件和账号操作，实现幂等适配 |
| `internal/domain.Manager` | 保留 Postfix/DKIM 操作，补充命令结果映射和对账 |
| `internal/forward` | 保留扫描、转发和生命周期实现 |
| `internal/config.RemoteConfig` | 保留完整配置拉取和 revision Apply |
| `internal/filter` / `filterpolicy` | 保留现有行为，只替换身份鉴权与通知入口 |
| `internal/handler.NodeHandler` | 迁移期继续服务 legacy HTTP；业务逻辑逐步下沉供 gRPC 复用 |

### 6.3 新增模块建议

```text
node-contract/
  go.mod
  proto/mailhub/node/v1/node.proto
  gen/...                         # 受版本控制的生成代码

mgmt-system/internal/nodeauth/   # 邀请、节点凭证校验
mgmt-system/internal/nodecommand/# 命令状态机与持久化
mgmt-system/internal/nodesession/# Control/Data 会话注册表
mgmt-system/internal/nodetransport/
  transport.go
  legacy_http.go
  control_stream.go
mgmt-system/internal/nodegateway/# gRPC 服务端

mail-node/internal/identity/     # UUID 和凭证文件
mail-node/internal/agent/        # 重连、握手、Control/Data streams
mail-node/internal/command/      # dispatcher、幂等记录、结果
```

`node-contract` 与现有 `filter-contract` 一样作为本地 Go module，被 mgmt 和 node 同时引用，避免两边复制协议结构。

---

## 7. 数据模型

### 7.1 `mail_servers` 兼容扩展

新增字段建议：

```text
node_uuid             CHAR(36) NULL UNIQUE
enrollment_state      VARCHAR(24) NOT NULL
connection_state      VARCHAR(24) NOT NULL
readiness_state       VARCHAR(24) NOT NULL
allocation_state      VARCHAR(24) NOT NULL
transport_mode        VARCHAR(24) NOT NULL
lease_expires_at      DATETIME NULL
agent_version         VARCHAR(64)
protocol_version      VARCHAR(32)
capabilities_json     JSON/TEXT
mail_public_ips_json  JSON/TEXT
last_connected_at     DATETIME NULL
last_disconnected_at  DATETIME NULL
```

兼容默认值：

```text
历史节点：
  enrollment_state = legacy_approved
  connection_state = unknown
  readiness_state = 由旧 status 映射
  allocation_state = active
  transport_mode = legacy_http
```

迁移阶段保留 `api_host`、`smtp_host`、`imap_host` 和 `public_host`。`api_host` 不再是身份唯一键，但在所有节点离开 legacy 模式前不能删除。

### 7.2 `node_enrollment_tokens`

```text
id
token_prefix
token_hash
expected_node_uuid NULL
name/environment/region/labels
state                  active/used/expired/revoked
expires_at
max_uses/used_count
created_by/created_at/revoked_at
```

### 7.3 `node_enrollment_requests`

```text
id
enrollment_token_id
request_secret_hash
requested_node_uuid
requested_name
hostname/os/arch/agent_version
source_ip
state                  pending/approved/rejected/completed/expired
reviewed_by/reviewed_at/review_note
server_id NULL
created_at/updated_at
```

待审批 node 不能进入 `mail_servers` 可分配集合。批准操作在一个事务内创建或绑定 server、创建节点凭证并推进 request 状态。

### 7.4 `node_credentials`

本轮使用每节点 Token：

```text
id
server_id
credential_prefix
credential_hash
state                  active/rotating/revoked/expired
version
expires_at
last_used_at
created_at/revoked_at
```

完整 credential 只通过受 request secret 保护的领取响应返回一次。数据库不保存明文。

### 7.5 `node_commands`

```text
command_id             UUID
server_id
sequence               节点内单调递增
command_type
schema_version
idempotency_key
payload_json
state                  queued/delivered/received/running/succeeded/
                       succeeded_with_warning/failed/rejected/expired
attempt_count
deadline_at
received_at/started_at/finished_at
result_code/result_json/error_message
trace_id/requested_by
created_at/updated_at
```

唯一约束：

```text
unique(server_id, sequence)
unique(server_id, idempotency_key)
```

NR-D14 已明确本轮不加密 `payload_json`。因此必须限制命令表访问、禁止日志打印 payload，并设置完成命令的结果/负载保留策略。

### 7.6 注册与凭证 API

管理端 Session API：

```text
POST /api/v1/admin/node-enrollments
GET  /api/v1/admin/node-enrollments
POST /api/v1/admin/node-enrollments/:id/revoke

GET  /api/v1/admin/node-enrollment-requests
GET  /api/v1/admin/node-enrollment-requests/:id
POST /api/v1/admin/node-enrollment-requests/:id/approve
POST /api/v1/admin/node-enrollment-requests/:id/reject

POST /api/v1/admin/servers/:id/credentials/rotate
POST /api/v1/admin/servers/:id/credentials/revoke
```

注册 bootstrap API：

```text
POST /api/v1/node-enrollments/claim
GET  /api/v1/node-enrollments/requests/:id
POST /api/v1/node-enrollments/requests/:id/complete
```

安全约束：

- `claim` 使用 Enrollment Token，完整值不进入 URL、访问日志或响应回显。
- `claim` 返回 request ID 和一次性 request secret；system 只保存 request secret 哈希。
- pending 查询和 `complete` 使用 request secret，不使用管理员 Session 或全局 shared secret。
- `complete` 只允许成功一次，并返回完整节点运行 Token；system 只保存运行 Token 哈希。
- approve/reject/rotate/revoke 必须写管理员审计。
- 来源 IP 只做审计，不参与身份判断。
- 生产默认人工批准；auto approve 必须显式启用且受邀请约束。

迁移期 `/api/v1/internal/*` 采用双鉴权：

```text
legacy_http 节点：X-Internal-Token: <shared_secret>
dual/control_stream 节点：Authorization: Node <credential>
                         X-MailHub-Node-UUID: <uuid>
```

使用节点 credential 的请求必须从凭证映射出 server，并校验 URL/body/query 中出现的 server ID 与其一致，防止节点读取或上报其他节点数据。

### 7.7 system UI

服务器池增加：

- 注册邀请列表和创建抽屉；
- 完整 Enrollment Token 单次展示；
- 待审批节点列表和详情；
- approve/reject 操作；
- UUID、transport、四维状态、lease、Agent/协议版本和 capabilities；
- 节点 credential 前缀、到期、轮换和撤销；
- 最近命令、失败和重试状态；
- `legacy_http / dual / control_stream` 灰度切换。

所有新增文字进入现有中英日 i18n，不在 JSX 中硬编码。

---

## 8. 协议契约

### 8.1 gRPC 服务

```proto
service NodeGateway {
  rpc Control(stream NodeControlFrame) returns (stream SystemControlFrame);
  rpc Data(stream NodeDataFrame) returns (stream SystemDataFrame);
}
```

两条流均由 node 发起。认证 metadata：

```text
authorization: Node <credential>
x-mailhub-node-uuid: <uuid>
```

服务端从 credential 映射 server，不信任消息体自行声明的 `server_id`。

### 8.2 ControlStream 消息

node -> system：

```text
Hello
Heartbeat
CommandReceived
CommandStarted
CommandResult
ConfigApplied
NodeEvent
Ping/Pong
```

system -> node：

```text
Welcome
Command
ConfigRevisionChanged
FilterRevisionChanged
CancelCommand
Ping/Pong
DrainNotice
```

握手成功前只允许 `Hello`。一个 UUID 只允许一个 active control session；新 session 接管前必须使旧 session 失效并记录原因。

### 8.3 DataStream 消息

```text
NodeDataFrame
  DataStreamHello（首帧，关联 active Control session）
  NodeDataHeader / NodeDataChunk / NodeDataEnd / NodeDataError
  Ping / Pong

SystemDataFrame
  DataStreamWelcome
  SystemDataRequest / CancelDataRequest
  Ping / Pong

SystemDataRequest
  request_id
  type
  mailbox/message/attachment locator
  deadline

NodeDataHeader
  status/content_type/content_length/content_disposition

NodeDataChunk
  request_id/sequence/bytes

NodeDataEnd
  checksum/total_bytes

CancelDataRequest
```

限制：

- 每帧建议不超过 256 KiB。
- 每节点设置并发流、单请求大小、总带宽和空闲超时。
- system 下游客户端断开时必须取消 node 上游读取。
- DataStream 阻塞不能导致 ControlStream 心跳超时。
- DataStream 握手成功前只允许 `DataStreamHello`；Hello 必须携带当前 `control_session_id`。

### 8.4 版本与能力

Hello 上报：

```text
agent_version
supported_protocol_versions
capabilities
boot_id/started_at
last_acked_sequence
desired_revision/applied_revision
```

system 选择共同协议版本。没有共同版本时拒绝会话，但不撤销节点身份。

### 8.5 第一批命令与查询类型

ControlStream 命令：

| 类型 | 幂等键建议 | 结果重点 |
|---|---|---|
| `domain.apply.v1` | `domain:apply:<domain>` | Postfix/DKIM 分项状态、DNS records |
| `domain.remove.v1` | `domain:remove:<domain>` | 本地邮箱防御检查和删除结果 |
| `domain.inspect.v1` | `domain:inspect:<domain>:<request>` | 实际 Postfix/DKIM 快照 |
| `mailbox.create.v1` | `mailbox:create:<email>` | 已存在一致视为成功，冲突单独返回 |
| `mailbox.password.v1` | `mailbox:password:<email>:<operation>` | Dovecot 配置 Apply 结果 |
| `mailbox.delete.v1` | `mailbox:delete:<email>` | `.trash` 路径和幂等删除结果 |
| `mailbox.restore.v1` | `mailbox:restore:<email>:<operation>` | 恢复或路径冲突 |
| `message.delete.v1` | `message:delete:<mailbox>:<message>` | 已不存在视为幂等成功 |
| `message.retention.purge.v1` | `retention:<server>:<window>` | 删除和失败计数 |
| `quarantine.release.v1` | 复用现有 operation ID | release receipt 和状态 |
| `quarantine.gc.v1` | `quarantine-gc:<server>:<window>` | expired keys |

通知不是持久化业务命令的替代品：

```text
config.revision.changed.v1
filter.revision.changed.v1
```

它们只唤醒 node 拉取完整期望状态；通知丢失后由周期 revision 对账恢复。

DataStream 请求：

```text
message.list.v1
message.body.v1
message.raw.v1
message.attachment.v1
message.attachment.preview.v1
quarantine.message.v1
quarantine.attachment.v1
```

只读 Data 请求不写入长期命令队列，节点离线或 deadline 到期时快速失败。

---

## 9. 节点状态与分配

状态分拆：

| 维度 | 值 |
|---|---|
| enrollment | pending / approved / revoked / legacy_approved |
| connection | connected / disconnected / unknown |
| readiness | ready / degraded / failed / unknown |
| allocation | active / draining / disabled |

新邮箱可分配条件：

```text
enrollment in (approved, legacy_approved)
AND connection 可用于当前 transport_mode
AND readiness = ready
AND allocation = active
AND current_load < capacity
AND server_domain.status = active
AND server_domain.postfix_status = synced
```

`legacy_http` 节点沿用旧探测结果；`control_stream` 节点必须同时有 active session 和未过期 lease；`dual` 节点按主 transport 判断，不能把两个信号简单 OR 后掩盖故障。

连接断开立即停止新分配。已有 Postfix、Dovecot、Maildir、过滤和转发继续工作。

---

## 10. 域名命令边界

### 10.1 添加域名

```text
system BindServerDomain(pending)
  -> domain.apply.v1
  -> node 调用现有 domain.Manager.AddDomain
  -> 返回 DomainSetup
  -> system 更新 server_domain
```

结果映射：

| Postfix | DKIM | command | sync_status |
|---|---|---|---|
| synced | synced | succeeded | synced |
| synced | sync_failed | succeeded_with_warning | partial |
| sync_failed | 未执行/失败 | failed | sync_failed |

必须保留 `dkim_selector / dkim_public_key / dkim_error / dns_records`。

### 10.2 删除域名

system 先检查该 server/domain 没有邮箱，再下发 `domain.remove.v1`。node 仍需执行本地 mailbox 防御检查。命令成功后 system 才标记 binding removed。

### 10.3 DNS 地址

不再从 `api_host` 生成 DNS A 记录。使用：

```text
public_host + mail_public_ips
```

来源 IP、ControlStream 远端地址和 NAT 出口地址都不能自动成为邮件 DNS 地址。

---

## 11. 实施阶段

### NR-P0：基线与协议冻结

状态：已完成（2026-07-24）。

交付：

- 固定本计划的路由清单、命令类型和状态枚举。
- 记录三个 Go module、Web i18n/UI contract/build 的基线。
- 建立 `node-contract` module 和协议兼容测试。
- 引入并固定 `grpc-go`、Protobuf runtime、`protoc-gen-go` 和 `protoc-gen-go-grpc` 版本；生成代码纳入版本控制。
- 提供单一协议生成命令并校验重复生成无 diff，禁止开发者手工编辑生成文件。
- 增加 feature flags，但默认全部关闭。

验收：当前 legacy 部署行为和测试无变化。

固定版本和基线记录见[NR-P0 基线与协议冻结记录](node-registration-p0-baseline.md)。

### NR-P1：身份、地址和数据库模型

状态：已完成（2026-07-24）。

交付：

- node UUID 生成、原子持久化、权限校验和克隆检测基础。
- `mail_servers` 兼容字段和四维状态。
- enrollment、request、credential、command 表。
- `public_host / mail_public_ips / smtp_host / imap_host` 管理契约。
- AutoMigrate 和旧数据兼容测试。

验收：旧节点无需 UUID 仍能按 legacy 模式工作；新字段不改变旧分配结果。

实现与验证记录见 [NR-P1 身份、地址与兼容 Schema 验收记录](node-registration-p1-identity-schema.md)。

### NR-P2：注册、审批和每节点凭证

状态：已完成（2026-07-24）。

交付：

- 管理端创建/撤销邀请、查看待审批、批准/拒绝。
- node `identity init/show` 与 `enroll/enroll resume`。
- request secret 轮询和单次领取节点 credential。
- system UI、三语 i18n、审计和 Token 脱敏。
- 新的 node credential middleware，双鉴权兼容 shared secret。

验收：标准注册、预绑定 UUID、过期、重复使用、拒绝、撤销、重装恢复均有测试。

实现与验证记录见 [NR-P2 注册、审批与每节点凭证验收记录](node-registration-p2-enrollment.md)。

### NR-P3：NodeTransport 收口

状态：已完成（2026-07-25）。

交付：

- 定义 `NodeTransport`。
- 实现 `LegacyHTTPTransport`，保持现有状态码、JSON 和二进制头语义。
- 把第 6.1 节所有直接 HTTP 调用迁入 transport。
- 增加静态检查，禁止业务包新增直接 node URL 拼接。

验收：仍只使用 legacy HTTP 时，全量行为与改造前一致。

实现与验证记录见 [NR-P3 NodeTransport 收口设计与验收记录](node-registration-p3-transport.md)。

### NR-P4：ControlStream、会话和 lease

状态：已完成（2026-07-25）。

交付：

- gRPC Control 服务、node Agent、认证、Hello/Welcome。
- 指数退避和 jitter 重连。
- 单节点 active session、lease 和四维状态更新。
- revision 通知、心跳、本地组件状态。
- `ControlStreamTransport` 基础实现。

验收：NAT 后 node 仅出站即可 connected；断线后停止新分配，重连恢复。

实现与验证记录见 [NR-P4 ControlStream、会话与 lease 验收记录](node-registration-p4-control-stream.md)。

### NR-P5：持久化命令迁移

状态：已完成（2026-07-25）。

迁移顺序：

1. 配置、过滤 revision 通知。
2. 域名添加、删除和 inspect。
3. 邮箱创建、改密、删除、恢复。
4. 生命周期删除重试、消息保留和隔离 GC。
5. 隔离区 release/status。

交付：

- 命令状态机、sequence、ACK、deadline、重投。
- node 本地幂等结果记录和保留策略。
- 同步 API 等待适配器和 operation/request ID。
- 域名 `succeeded_with_warning` 与现有 partial 状态映射。

验收：断线、system 重启、node 重启、重复投递均不产生重复业务副作用。

实现与验证记录见 [NR-P5 持久化命令迁移验收记录](node-registration-p5-durable-commands.md)。

### NR-P6：DataStream 迁移

状态：已完成（2026-07-25）。

迁移顺序：

1. 邮件列表和正文。
2. raw EML。
3. 附件和预览。
4. 隔离区原件和附件。

交付：

- DataStream session、请求路由、chunk、取消、限流。
- HTTP 下游响应头和状态码兼容适配。
- Control/Data 隔离压测。

验收：大附件传输时心跳和控制命令延迟不越过阈值，客户端取消能停止 node 读取。

实现与验证记录见 [NR-P6 DataStream 迁移验收记录](node-registration-p6-data-stream.md)。

### NR-P7：dual 灰度与关闭 8081

状态：代码完成（节点切换、fleet preflight、dual 影子读取、Legacy 节点原位注册迁移、凭证解耦和 control_stream HTTP 关闭已实现；真实远程验收待执行）。

交付：

- 每节点 `legacy_http / dual / control_stream` 切换，事务内写审计。
- `GET /api/v1/admin/servers/transport-preflight` 提供 fleet 级切换前检查。
- dual 模式查询执行有界 legacy 影子读取并比较状态码和正文哈希，变更命令单主通道，禁止双写。
- control_stream 节点的配置、过滤、生命周期、outbox、heartbeat 请求统一使用节点凭证。
- control_stream 节点不启动本地 `8081` HTTP listener。
- 逐节点 canary、回滚演练和运维文档转正。
- 所有节点切换后关闭 system -> node `8081` 网络访问。
- shared secret 只为未迁移 legacy 节点保留，最终删除。

验收：满足第 14 节完成定义。

第一切片记录见 [NR-P7 dual 灰度与 legacy 回滚记录](node-registration-p7-canary-rollback.md)。

### 远程发布里程碑

| 阶段 | 可验证范围 | 发布结论 |
|---|---|---|
| NR-P4 | NAT 后 node 仅出站建连、心跳、lease 和状态恢复 | 技术演示，不是业务 MVP |
| NR-P6 | ControlStream、持久化命令和全部 DataStream 业务路径可用 | 可进入远程 staging，视为功能 MVP 候选 |
| NR-P7 | dual canary、回滚演练、关闭 system -> node `8081`、移除 control_stream 节点 shared secret，完成第 14 节验收 | 可生产发布的 MVP 完成 |

NR-P6 已达到远程 staging 的功能 MVP 候选范围；在 NR-P7 完成 dual canary、回滚演练、关闭 `8081` 和 shared secret 清理前，不得描述为可生产发布的 MVP。

### 后续安全加固：mTLS

节点注册主线完成后单独设计内部 CA、CSR、证书签发、轮换和撤销。不得在本轮中途把 Token 与证书两套未完成实现混在同一个鉴权路径。

---

## 12. Feature Flags 与配置

### 12.1 mgmt-system

建议配置：

```yaml
node_control:
  enabled: false
  listen: ":8443"
  public_url: "https://node-control.example.com:443"
  tls_cert_file: ""
  tls_key_file: ""
  heartbeat_interval_seconds: 30
  lease_timeout_seconds: 90
  command_timeout_seconds: 15
  data_max_concurrency_per_node: 4
  data_chunk_size: 262144
  legacy_http_enabled: true
```

生产可以通过 L4/L7 网关把专用 node-control 域名的 443 转发到内部 gRPC listener。是否与管理后台共用域名不进入业务代码假设。

### 12.2 mail-node

建议配置：

```yaml
management:
  api_url: "https://mailhub.example.com"
  control_url: "node-control.example.com:443"
  transport_mode: "legacy_http"
  credential_file: "/var/lib/mail-node/identity/credential"
  ca_file: "/etc/mail-node/management-ca.pem"

identity:
  directory: "/var/lib/mail-node/identity"
```

迁移期保留 `server.advertise_host`、`node.id` 和 `shared_secret`，但 control_stream 模式不得读取它们作为身份。

---

## 13. 测试策略

### 13.1 单元测试

- UUID 初始化幂等、文件权限和损坏文件拒绝。
- 邀请 Token 哈希、过期、次数、撤销和预绑定 UUID。
- 凭证校验、轮换、撤销和节点映射。
- command 状态机非法跳转和条件更新。
- sequence、idempotency、deadline 和重复结果。
- 四维状态与可分配谓词。
- 域名 partial 结果映射。
- Data chunk 顺序、大小、取消和响应头映射。

### 13.2 集成测试

使用 gRPC `bufconn` 或本地 TLS listener 覆盖：

- 注册 -> 审批 -> 领取 credential -> 建连。
- 错误 UUID、错误 credential、撤销 credential。
- system/node 任意一侧重启后的重连。
- command 在 delivered/received/running 各阶段断线。
- 重复命令不重复创建邮箱或删除域名。
- 配置通知丢失后 revision 对账恢复。
- 附件下载与心跳并发。

### 13.3 回归测试

- `mgmt-system`: `go test ./...`
- `mail-node`: `go test ./...`
- `filter-contract`: `go test ./...`
- 新 `node-contract`: `go test ./...`
- Web i18n、UI contract 和生产构建。
- legacy、dual、control_stream 三模式矩阵。

广告过滤只做“不回归”验证：现有模式、bundle 拉取、状态上报、decision outbox 和隔离区链路继续工作；不新增评分或策略验收。

---

## 14. 完成定义

只有同时满足以下条件，本轮改造才算完成：

1. 新节点可通过邀请、UUID 和审批完成注册，system 不需要预先知道 node IP。
2. 每个 node 使用独立凭证；撤销单节点不影响其他节点。
3. node 仅通过出站网络建立 ControlStream 和 DataStream。
4. 所有 system -> node 业务调用都经过 `NodeTransport`。
5. control_stream 节点不依赖 `api_host` 执行管理操作。
6. 新分配严格检查 enrollment、connection、readiness、allocation 和 domain Postfix 状态。
7. 域名挂载保留 Postfix/DKIM partial 语义和 DNS 清单。
8. 命令在断线、重启和重复投递下保持幂等并可恢复。
9. 邮件正文、raw EML、附件、预览和隔离区读取完成 DataStream 迁移。
10. 大文件流不会阻塞心跳和控制命令。
11. node 离线期间已有 SMTP、IMAP、Maildir、过滤和转发继续运行。
12. dual 模式逐节点 canary 和回滚演练通过。
13. 防火墙关闭 system -> node `8081` 后完整业务验收通过。
14. 旧 shared secret 已从全部 control_stream 节点移除。
15. 注册帮助文档从“目标指南”更新为当前可执行指南。
16. 当前广告过滤模式和数据无回归，但不要求完成广告过滤项目。

---

## 15. 回滚边界

- schema 先加后删，本轮不删除 legacy 字段。
- UUID、注册审批、凭证撤销和已经完成的命令结果不能因 transport 回滚而倒退。
- transport 可按节点从 `control_stream -> dual -> legacy_http` 回退。
- dual 模式中只有一个变更主通道，避免回滚时重复执行。
- DataStream 可按功能回退到 legacy HTTP，但 ControlStream 状态不能伪装成业务读取成功。
- 广告过滤暂停状态不因节点 transport 回滚而自动切换模式或 revision。

---

## 16. 开工顺序

下一次开始编码时严格从 NR-P0 开始：

```text
1. 建 node-contract 和协议测试
2. 加兼容 schema 与模型测试
3. 实现 node identity
4. 实现 enrollment store/service/API
5. 实现 system 注册审批 UI
6. 收口 LegacyHTTPTransport
7. 再实现 gRPC streams
```

不得直接从 gRPC handler 开始改域名或邮箱业务，否则 transport、身份和业务状态会再次耦合。
