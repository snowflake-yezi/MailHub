# MailHub 全链路审查修复计划

> 日期：2026-07-16
> 范围：`mgmt-system`、`mail-node`、管理端调用契约、控制面与数据面通信
> 状态：SEC-1 至 SEC-4、REL-1 已完成并发布；其余风险项暂停

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
| SEC-3 | P1 | SMTP TLS 默认跳过证书校验 | 新安装默认 `false`，节点 fallback 同步调整 | 默认配置构造出的 TLS 配置启用证书校验 | 已完成并发布 |
| SEC-4 | P1 | 废弃 `/smtp/filter` 公开且无限读取 | 从公开路由移除；全局请求体限制为 16 MiB | 节点启动路由不再暴露该路径 | 已完成并发布 |
| REL-1 | P0 | SMTP 成功后 move 失败导致重复转发 | `moveToCur` 返回错误；成功投递但提交失败时改名为隔离文件并跳过后续扫描 | move 失败不会把邮件留作下一轮正常投递；错误可观察 | 已完成并发布 |
| REL-2 | P0 | postmap/postfix/doveadm 失败被吞掉 | 命令包装为可注入执行器并传播 stderr/exit error | 任一命令失败时节点返回失败，删除不会继续移动 Maildir | 已完成（SEC-2 配套） |
| REL-3 | P1 | 配置文件并发读改写覆盖 | mailbox/domain Manager 对配置变更串行化；整文件写使用唯一临时文件和原子替换 | 20 路并发创建不丢配置行；精确删除不误删相似邮箱 | 已完成（SEC-2 配套） |
| CFG-1 | P1 | filter 动态配置只在启动时生效 | Engine 在 revision 提交后于锁内更新默认动作和前缀 | reload 后下一封邮件使用新值 | 待验收（已暂停） |
| CFG-2 | P1 | `filter_sync_interval=0` 触发 ticker panic | Load 设置默认值，StartAutoSync 再做下限保护 | 缺省/非正值不 panic | 待验收（已暂停） |
| OPS-1 | P1 | HTTP 服务无超时边界 | 使用显式 `http.Server`，设置 header/read/write/idle timeout | 两个入口不再使用裸 `r.Run` | 待验收（已暂停） |

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

SMTP 无法与本地文件系统组成原子事务，因此系统继续采用 at-least-once 语义。REL-1 在 SMTP 明确成功但本地 move 失败时，将原文件改名为 `.forwarded-error` 隔离文件；扫描器跳过该后缀，避免下一轮确定性重复投递，并通过 `delivered but commit failed` 错误保留可观察性。SMTP 返回结果不确定的网络故障仍可能产生一次重复，这是协议层残余风险。

## 4. 验证矩阵

- 定向单元测试：Token 退役、邮箱/密码/域名校验、命令失败传播与配置并发写入。
- 后端全量：两个模块分别运行 `go test ./...` 与 `go vet ./...`。
- 竞态：两个模块分别运行 `go test -race ./...`。
- 前端：运行生产构建，确认 API 契约未破坏。
- 工作区：不覆盖审查前已有的未跟踪文件。

## 5. 发布注意事项

1. 首次升级启动后确认 `api_tokens` 已删除、外部调用正常，再从配置文件移除 `auth.tokens` 明文项。
2. SEC-2 发布后确认两个节点健康、配置 revision 一致，并验证非法邮箱、密码和域名被控制面与节点同时拒绝。
3. SEC-3、SEC-4 已单独验收发布；后续项目继续逐项验收、独立提交和发布。
4. REL-1 已在两台 mail-node 独立备份并原子发布；后续发现 `.forwarded-error` 时必须结合 `delivered but commit failed` 日志人工核对，不得直接重新投递或删除。

## 6. 本地验证记录

2026-07-16 已完成：

- `mgmt-system`: `go test -count=1 ./...`、`go vet ./...` 通过。
- `mail-node`: `go test -count=1 ./...`、`go vet ./...` 通过。
- `mgmt-system/web`: Vite 生产构建通过，产物写入临时目录。
- Token 退役：覆盖无旧表回退、禁用旧记录不复活、重复导入幂等、验证失败不删表、验证成功后删表，以及退役后配置不得签发新 Token。
- SEC-2 注入矩阵：控制面和节点均覆盖路径分隔符、`..`、CR/LF/NUL、冒号密码、DNS label 与长度边界；实际 Mailbox Manager 入口同步验证。
- SEC-2 配套场景：命令失败传播和 mailbox/domain 配置并发写入均有自动化覆盖。
- SEC-3：控制面 schema、种子值和节点 fallback 均默认 `tls_insecure_skip=false`；现有显式配置值不会被种子更新覆盖。
- SEC-4：Gin 路由表不再包含 `/smtp/filter`，`/internal/*` 保持注册；请求体限制测试确认超过 16 MiB 时读取被拒绝。
- SEC-3/SEC-4 生产发布：控制面与两台 mail-node 均完成备份和原子替换；控制面 health/ready 为 200，两台节点 healthy 且 revision 一致，近 100 行日志无 panic/fatal。
- SEC-3/SEC-4 生产实测：两台节点的 `/smtp/filter` 从发布前 POST 200 变为发布后 404；携带本机 shared secret 的 `/internal/health` 返回 200，17 MiB JSON 请求在绑定阶段被拒绝。
- SEC-3/SEC-4 回滚点：主机 `/opt/mgmt-system/backups/sec3-sec4-e1a6aa1-20260716-094254`，节点 2 `/root/mailhub-backups/sec3-sec4-e1a6aa1-20260716-094936`。
- REL-1：定向测试复现隔离文件会被旧扫描器重新处理；修复后覆盖 move 失败传播、同目录 `.forwarded-error` 隔离、后续扫描跳过，以及跨盘复制关闭源文件后再删除。
- REL-1 发布门禁：两个 Go 模块的普通全量测试、`go vet ./...` 和 `go test -race -count=1 ./...` 均通过；Linux/amd64 mail-node 构建成功，SHA256 为 `4acca650ef2eb5b2bf97ab4d914a0b8fe726182f23efb9b1381c1def5bf525a1`。
- REL-1 生产发布：提交 `9114cba` 已进入远端 `main`，两台 mail-node 均使用上述 SHA256；回滚点分别为 `/opt/mgmt-system/backups/rel1-9114cba-20260717-014749` 和 `/root/mailhub-backups/rel1-9114cba-20260717-013700`。
- REL-1 生产验收：控制面 health/ready 均为 200；两台节点均为 healthy，revision 分别为 `2/2`、`1/1`，未鉴权内部健康请求均为 401；最近 100 行日志的 panic/fatal 和 `delivered but commit failed` 均为 0，发布后 `.forwarded-error` 数量均为 0。
- 第二节点发布前已有 7 封历史测试邮件因 SMTP 主机名无法解析而每轮发送失败；发布后仍保持 `processed=0 errors=7`，错误发生在 SMTP 拨号阶段，早于 REL-1 提交路径，不属于本次回归。
- 生产数据库已验证 `api_tokens` 物理删除，两个迁移后的哈希凭证保持启用，`auth.tokens` 已从运行配置移除。
- 两个模块的 `go test -race -count=1 ./...` 已通过。
- CFG-1、CFG-2 和 OPS-1 仍保持暂停，未进入本次实现范围。
