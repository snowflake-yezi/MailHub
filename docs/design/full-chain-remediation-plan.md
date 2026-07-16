# MailHub 全链路审查修复计划

> 日期：2026-07-16
> 范围：`mgmt-system`、`mail-node`、管理端调用契约、控制面与数据面通信
> 状态：已完成代码修复与本地回归

## 1. 修复目标

本轮修复针对全链路审查确认的问题，目标是：

1. 撤销、禁用和过期的 API 凭证必须立即失效，兼容逻辑不得绕过新鉴权状态。
2. 所有进入 Maildir、Postfix、Dovecot 和 OpenDKIM 的标识必须先完成严格校验。
3. 节点不得把系统命令执行失败报告为同步成功。
4. 邮件投递成功后，本地状态提交失败不得形成每轮扫描重复转发。
5. 动态配置的声明、实际应用和运行快照必须一致。
6. 服务入口具备合理的请求大小和连接超时边界。

## 2. 修复清单

| ID | 优先级 | 问题 | 修复策略 | 验收条件 | 状态 |
|---|---|---|---|---|---|
| SEC-1 | P0 | 迁移 Token 撤销后回退旧明文 Token | 将有效旧 Token 一次性导入并验证，随后删除 `api_tokens`；运行期只查询哈希凭证 | 禁用应用、撤销凭证、凭证过期均返回 401；数据库不存在明文 Token 表 | 已完成 |
| SEC-2 | P0 | 邮箱、密码、域名可注入路径/配置行 | 控制面和节点双层校验；限制 local-part、DNS 域名和控制字符；所有 Maildir 入口复用同一解析器 | `..`、路径分隔符、CR/LF、冒号密码均被拒绝 | 已完成 |
| SEC-3 | P1 | SMTP TLS 默认跳过证书校验 | 新安装默认 `false`，节点 fallback 同步调整 | 默认配置构造出的 TLS 配置启用证书校验 | 已完成 |
| SEC-4 | P1 | 废弃 `/smtp/filter` 公开且无限读取 | 从公开路由移除；全局请求体限制为 16 MiB | 节点启动路由不再暴露该路径 | 已完成 |
| REL-1 | P0 | SMTP 成功后 move 失败导致重复转发 | `moveToCur` 返回错误；成功投递但提交失败时改名为隔离文件并跳过后续扫描 | move 失败不会把邮件留作下一轮正常投递；错误可观察 | 已完成 |
| REL-2 | P0 | postmap/postfix/doveadm 失败被吞掉 | 命令包装为可注入执行器并传播 stderr/exit error | 任一命令失败时节点返回失败，删除不会继续移动 Maildir | 已完成 |
| REL-3 | P1 | 配置文件并发读改写覆盖 | mailbox/domain Manager 对配置变更串行化；整文件写使用唯一临时文件和原子替换 | 20 路并发创建不丢配置行；精确删除不误删相似邮箱 | 已完成 |
| CFG-1 | P1 | filter 动态配置只在启动时生效 | Engine 在 revision 提交后于锁内更新默认动作和前缀 | reload 后下一封邮件使用新值 | 已完成 |
| CFG-2 | P1 | `filter_sync_interval=0` 触发 ticker panic | Load 设置默认值，StartAutoSync 再做下限保护 | 缺省/非正值不 panic | 已完成 |
| OPS-1 | P1 | HTTP 服务无超时边界 | 使用显式 `http.Server`，设置 header/read/write/idle timeout | 两个入口不再使用裸 `r.Run` | 已完成 |

## 3. 兼容性决策

### 3.1 旧 Token

- 升级启动时导入 `api_tokens` 中仍启用的 Token，禁用记录不会因配置残留而复活。
- 每个 Token 的哈希凭证验证成功后才删除 `api_tokens`；验证或删表失败会阻止服务启动。
- 旧表删除后，`api_credentials` 是唯一事实源；应用禁用、凭证撤销或到期均直接拒绝。
- 残留 `auth.tokens` 仅可匹配已经导入的哈希凭证，不能签发新凭证，升级确认后应从配置移除。

### 3.2 邮箱标识

- 当前业务邮箱 local-part 限定为 ASCII 字母、数字及 `._+-`，长度 1-64。
- local-part 不允许首尾为点、不允许连续点，不允许任何路径分隔符或控制字符。
- 域名按 DNS label 校验；拒绝空 label、`..`、下划线、控制字符和超过长度限制的值。
- Dovecot passwd-file 中的密码拒绝 CR、LF、NUL 和冒号，避免字段/行注入。

### 3.3 转发一致性

SMTP 无法与本地文件系统组成原子事务，因此系统继续采用 at-least-once 语义。此次修复消除“已经明确收到 SMTP 成功，但 move 失败后每轮重复发送”的确定性重复：邮件会进入带 `.forwarded-error` 后缀的隔离状态，记录告警并等待人工核对。SMTP 返回结果不确定的网络故障仍可能产生一次重复，这是协议层残余风险。

## 4. 验证矩阵

- 定向单元测试：Token 回退、邮箱/密码/域名校验、命令失败传播、转发隔离、filter apply、ticker 默认值。
- 后端全量：两个模块分别运行 `go test ./...` 与 `go vet ./...`。
- 竞态：环境具备 CGO 和 C 编译器时运行 `go test -race ./...`；当前 Windows 环境缺少 `gcc`，需在 Linux CI 补跑。
- 前端：运行生产构建，确认 API 契约未破坏。
- 工作区：不覆盖审查前已有的未跟踪文件。

## 5. 发布注意事项

1. 已有数据库中的 `forward.tls_insecure_skip=true` 不自动覆盖，升级后应在配置页显式改为 `false`，确认 SMTP 证书链正常后发布。
2. 首次升级启动后确认 `api_tokens` 已删除、外部调用正常，再从配置文件移除 `auth.tokens` 明文项。
3. 上线后监控 `[forward] delivered but commit failed` 日志和 `.forwarded-error` 文件。
4. Linux 发布流水线必须补跑 race test，并在真实 Postfix/Dovecot 测试节点验证命令失败传播。

## 6. 本地验证记录

2026-07-16 已完成：

- `mgmt-system`: `go test -count=1 ./...`、`go vet ./...` 通过。
- `mail-node`: `go test -count=1 ./...`、`go vet ./...` 通过。
- `mgmt-system/web`: Vite 生产构建通过，产物写入临时目录。
- Token 退役：覆盖无旧表回退、禁用旧记录不复活、重复导入幂等、验证失败不删表、验证成功后删表，以及退役后配置不得签发新 Token。
- SEC-2 注入矩阵：控制面和节点均覆盖路径分隔符、`..`、CR/LF/NUL、冒号密码、DNS label 与长度边界；实际 Mailbox Manager 入口同步验证。
- 其他定向场景：命令失败传播、并发配置写、转发隔离、filter 热更新、配置默认值均有自动化覆盖。
- 真实 MySQL/MariaDB 升级环境仍需验证旧数据导入、`api_tokens` 物理删除及现有外部调用连续性。
- `go test -race ./...`：当前 Windows 环境缺少 `gcc`，未完成；保留为 Linux CI 发布门禁。
