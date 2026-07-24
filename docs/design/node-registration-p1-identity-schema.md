# NR-P1 身份、地址与兼容 Schema 验收记录

> 状态：已完成
>
> 日期：2026-07-24
>
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 本阶段边界

NR-P1 只建立永久身份、兼容数据模型和邮件公开地址契约：

- 不启动 gRPC ControlStream/DataStream；
- 不改变 legacy HTTP 节点发现、心跳和邮箱分配查询；
- 不实现邀请、审批、凭证签发 API 或管理 UI，这些属于 NR-P2；
- 不在 mail-node 主进程启动时强制创建身份目录，避免旧部署因目录权限改变启动行为；
- 不进入 NodeTransport 和业务命令改造，这些分别属于 NR-P3 及后续阶段。

## 2. 兼容 Schema

`mail_servers` 新增可空唯一 `node_uuid`、四维状态、transport/lease/Agent 元数据和邮件公网 IP 列。`node_uuid` 使用 NULL 表示尚未领取永久身份，历史多节点不会因相同空字符串触发唯一索引冲突。

历史节点按以下规则幂等回填：

| 旧字段 | 新字段 |
|---|---|
| 任意历史节点 | `enrollment_state=legacy_approved` |
| 尚无出站会话 | `connection_state=unknown` |
| `healthy` / `draining` | `readiness_state=ready` |
| `degraded` | `readiness_state=degraded` |
| `down` | `readiness_state=failed` |
| `draining` | `allocation_state=draining` |
| 其他旧状态 | `allocation_state=active` |
| 任意历史节点 | `transport_mode=legacy_http` |

回填在每次 AutoMigrate 后安全重试，能够覆盖“字段已经添加、首次回填尚未完成”时进程退出的情况；已经变为 `disabled` 的 allocation 不会被启动回填覆盖。

新增四张只承载模型和约束的表：

- `node_enrollment_tokens`：邀请摘要、预绑定 UUID、约束和使用状态；
- `node_enrollment_requests`：申请 UUID、request secret 摘要、机器信息和审批状态；
- `node_credentials`：每节点凭证摘要、代次、到期和撤销状态；
- `node_commands`：持久命令、节点内 sequence、幂等键、deadline 和结果。

完整邀请、request secret、节点运行 Token 均没有明文字段。命令表明确使用 `payload_json` / `result_json`，并建立 `server_id+sequence` 和 `server_id+idempotency_key` 两个唯一约束。

## 3. mail-node 永久身份

新增 `mail-node/internal/identity`：

- 使用 `crypto/rand` 生成标准 UUIDv4；
- 以同目录 0600 临时文件写入并 fsync，再通过原子硬链接抢占 `node-id`，并发初始化只能产生一个最终身份；
- 已有 UUID 必须是规范小写 UUIDv4，损坏文件、符号链接和孤立 credential 均拒绝，不会静默换号；
- Unix 身份目录要求 0700 或更严格，身份文件要求 0600 或更严格；
- Linux 从 `/etc/machine-id` 或 `/var/lib/dbus/machine-id` 取稳定机器 ID，只保存 SHA-256 指纹；指纹不一致返回 clone detected，UUID 不变；
- 缺少稳定 machine ID 时仍可持久化 UUID，但不会伪造主机名指纹。后续 NR-P2 注册申请会把可用指纹提交给 system 做重复 UUID 检测。

机器指纹只能作为克隆告警信号，不是凭证。镜像若同时复制 identity 目录和未重新生成的 machine-id，仍需由 NR-P2/NR-P4 的 UUID、独立凭证和并发会话检查拦截。

## 4. 邮件公开地址契约

服务器管理 API 和管理端表单现在分别维护：

- `api_host`：仅供 legacy HTTP 控制访问；
- `smtp_host` / `imap_host`：邮件客户端服务地址；
- `public_host`：邮件 DNS 主机名；
- `mail_public_ips`：用于 DNS A/AAAA 的显式 IPv4/IPv6 列表。

新建 legacy 节点仍可从 `api_host` 初始化空的 SMTP/IMAP 地址。域名 DNS 清单不再从 `api_host`、请求来源 IP 或 NAT 出口推导地址，只使用规范化后的 `mail_public_ips`；`public_host` 可作为域名 MX/A 主机的管理默认值。

## 5. 验证证据

已通过：

- `mgmt-system`: 全量 `go test ./...`；
- `mail-node`: 全量 `go test ./...`，含 16 路并发 identity 初始化、损坏文件、孤立凭证和克隆检测；
- `filter-contract`、`node-contract`: 全量 `go test ./...`；
- 管理端 3 种语言、916 个键，UI contract 和 Vite production build；
- SQLite 兼容增量测试：两条历史节点保留原地址、容量和负载，两个 NULL UUID 共存，重复非 NULL UUID 被唯一索引拒绝，四张新表和命令唯一索引存在，重复迁移通过；
- identity Linux/amd64 测试二进制交叉编译通过。

当前 Windows 开发机没有已安装的 WSL distribution，Docker daemon 也未运行，因此本轮未在本机执行 Unix-only 权限测试二进制；测试已用 build tag 纳入 Linux 测试套件。按发布安排，Unix 权限测试与 MariaDB 真实克隆迁移验证统一延后到 NR-P7 发布验收阶段，并通过目标 Unix 环境及 `MAILHUB_TEST_MYSQL_DSN` 集成测试入口执行。
