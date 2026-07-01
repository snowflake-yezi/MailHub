# T10 收尾设计文档

> 版本: v1.0 | 日期: 2026-06-30 | 状态: 已实现
> 依据: `context.md` 当前续接顺序、`REQUIREMENTS_ANALYSIS.md` §2.1.1 / §2.1.3 / §2.4 / §4、`docs/design/phase3-mgmt-completion-plan.md` 的 P2 / P3 收尾项
> 范畴: 仅覆盖本次 T10 收尾的四项能力，不包含临时机清理

---

## 1. 背景与目标

T9 restore 已完成后，当前邮箱服务主链路已经闭环，但控制面和运维侧还剩几个明显收尾点：

- 过滤规则变更后仍主要依赖 mail-node 周期拉取，缺少 mgmt 主动通知
- 仪表盘缺少“今日创建数”这个最直接的运营指标
- 邮件正文查询的 `message_id` 需要补齐历史兼容性
- TLS / 证书 / 部署步骤需要统一收口到文档里，避免上线时靠记忆操作

本期目标是把这些收尾项做成“小而完整”的闭环，保持现有架构不变，只补齐最后一段可运维性。

**非目标**：
- 不做临时机清理
- 不改邮箱生命周期语义
- 不把 filter 同步改成完全推送替代轮询；保留轮询兜底
- 不引入新的后台页面结构

---

## 2. 需求拆分

### 2.1 filter 主动推送

**问题**：mgmt 对过滤规则的 CRUD 已经存在，但 mail-node 侧规则刷新主要靠 `StartAutoSync` 的周期拉取。规则变更后等待轮询，反馈慢且不直观。

**目标**：
- 规则新增、修改、删除后，mgmt 尽快通知相关 mail-node 刷新
- 保留既有周期拉取作为兜底，不把实时性完全压在单次 HTTP 通知上

**约束**：
- 通知必须走现有内部鉴权（`X-Internal-Token`）
- 通知失败不能阻塞规则 CRUD 主流程
- 不能引入新的配置分发通道

### 2.2 仪表盘“今日创建数”

**问题**：当前仪表盘只有服务器状态和活跃邮箱总数，缺少当天新增量，运营侧看不出当天创建节奏。

**目标**：
- 在仪表盘新增一个当天创建数指标
- 统计口径与现有账号台账一致，直接按 `mailbox_accounts.created_at` 聚合

**约束**：
- 不引入异步计数器或额外表
- 统计结果以 mgmt 服务器本地“今天”为准

### 2.3 `message_id` 编码兼容

**问题**：邮件正文查询链路里，`message_id` 在不同层有不同编码习惯。新链路已经做了 URL 编码，但历史邮件或旧实现里可能存在角括号、特殊字符、fallback id 等兼容问题。

**目标**：
- 保持当前路径编码安全
- 在 mail-node 查询侧补齐兼容命中逻辑
- 不改消息存储格式，不重写历史数据

**约束**：
- 兼容性修改只能落在查询侧
- 不扩大 `message_id` 的生成规则面

### 2.4 TLS / 证书 / 部署文档收口

**问题**：当前代码和部署文档已经分散记载了 submission、STARTTLS、Nginx 反代、证书等内容，但没有把当前服务上线的实际步骤收口成一份可直接执行的说明。

**目标**：
- 统一梳理当前邮件服务的 TLS 和部署步骤
- 把证书、reload、重启、验证步骤写成可操作的文档

**约束**：
- 优先写文档，再决定是否补代码配置项
- 如确有代码变化，只做最小配置开关补充

---

## 3. 方案设计

### 3.1 filter 主动推送

#### 3.1.1 mgmt 侧通知入口

在 `mgmt-system/internal/handler/filter.go` 中，为 `CreateRule`、`UpdateRule`、`DeleteRule` 增加一个统一的通知调用。

推荐做法：
- 规则写库成功后再触发通知
- 通知失败只写日志，不影响 CRUD 返回成功
- 通知逻辑封装成一个小 helper，避免三处复制

#### 3.1.2 通知目标

通知优先对现有 mail-node 内部接口发起 `POST /internal/filters/reload`。

理由：
- 现有 mail-node 已经暴露该接口
- 内部鉴权链路已存在
- 语义清晰，和周期拉取并不冲突

如果后续有多个节点，需要在 mgmt 的 `mail_servers` 当前节点列表上逐个通知；节点通知失败只记录告警，不影响规则保存结果。

#### 3.1.3 mail-node 侧行为

`mail-node/internal/handler/node.go:ReloadFilters` 目前只是兼容返回。T10 先不把它改成复杂逻辑，只保证它作为“收到 reload 通知”的稳定入口存在。

真实规则刷新仍由 `filter.Engine.SyncFromManager` 完成，主动推送只是触发更新，不改变规则加载数据源。

#### 3.1.4 失败语义

- mgmt 通知失败：记录 warning/error，规则 CRUD 返回成功
- mail-node reload 接口失败：同样不影响 mgmt 主流程
- 周期拉取继续作为最终兜底，确保最终一致性

---

### 3.2 今日创建数

#### 3.2.1 数据层

新增一个按日期统计的存储方法，直接对 `mailbox_accounts` 做 count 聚合。

推荐逻辑：
- 过滤 `status = active` 的邮箱，或按现有台账口径统计“已创建账号”
- 以 `created_at` 的本地日期范围统计当天增量
- 返回 `int64` 供仪表盘展示

#### 3.2.2 处理层

`mgmt-system/internal/handler/admin.go:Dashboard` 继续维持轻量聚合模式：
- 服务器列表：`ListServers()`
- 活跃邮箱总数：现有 `ListMailboxes()` 统计
- 今日创建数：新增 store 方法

这样不会把仪表盘升级成一个重查询页面，保持当前控制台风格即可。

#### 3.2.3 前端展示

`mgmt-system/template/admin/dashboard.html` 的指标卡区域新增一个卡片，和“服务器健康/总数”“活跃邮箱数”并列。

展示建议：
- 主值：今日创建数
- 副标题：今天新建邮箱

不改页面布局，只增加一个卡片。

---

### 3.3 `message_id` 兼容

#### 3.3.1 当前链路

- mgmt 已经在代理请求时做 `url.PathEscape(messageID)`
- mail-node 在 `GetMessageBody` 里做 `url.PathUnescape`
- 当前缺的是对历史/旧格式 message id 的兼容查找

#### 3.3.2 兼容策略

只在 mail-node 查询侧做兼容，建议按以下顺序查找：
1. 直接按收到的 `message_id` 匹配
2. 按 URL 解码后的值匹配
3. 按去掉 `< >` 的规范化值匹配
4. 必要时对 fallback 格式做轻量归一

这样可以覆盖：
- 带角括号的 RFC Message-ID
- 被 URL 编码过的路径值
- 历史 fallback ID

#### 3.3.3 不做的事

- 不修改入库 message id 的格式
- 不批量迁移历史邮件
- 不新增独立索引结构

---

### 3.4 TLS / 证书 / 部署收口

#### 3.4.1 文档范围

先把以下内容整理成统一可执行步骤：
- 当前收信/发信链路里 TLS 的位置
- Nginx 反代与证书的关系
- 证书更新后的 reload / restart 顺序
- go binary、systemd、Nginx 的部署顺序
- 验证步骤：登录、发信、内部 API、邮件查询

#### 3.4.2 代码范围

如果文档梳理后发现确实需要配置项，再最小化补充：
- `mail-node/internal/forward/smtp.go`
- `mail-node/internal/config/config.go`

目前优先判断是否只是文档缺失，而不是程序缺失。

#### 3.4.3 写入位置

建议更新：
- `docs/design/deployment-guide.md`
- `DEPLOY.md`

如果需要，也可在 `context.md` 追加本次结论，但不作为主交付物。

---

## 4. 代码与文档变更清单

### 4.1 代码

| 文件 | 变更 |
|------|------|
| `mgmt-system/internal/handler/filter.go` | CRUD 成功后触发 mail-node reload 通知 |
| `mgmt-system/internal/store/store.go` | 新增今日创建数统计方法 |
| `mgmt-system/internal/handler/admin.go` | Dashboard 注入今日创建数 |
| `mgmt-system/template/admin/dashboard.html` | 新增指标卡 |
| `mail-node/internal/handler/node.go` | 保持 reload 接口作为稳定入口；必要时补兼容行为 |
| `mail-node/internal/handler/message_parser.go` | 仅在查询兼容需要时补辅助逻辑 |
| `mail-node/internal/forward/smtp.go` | 仅在 TLS 配置需要时补最小开关 |
| `mail-node/internal/config/config.go` | 仅在新增 TLS 配置项时修改 |

### 4.2 文档

| 文件 | 变更 |
|------|------|
| `docs/design/deployment-guide.md` | 补齐当前服务 TLS / reload / 部署步骤 |
| `DEPLOY.md` | 补齐当前生产部署与证书收口说明 |
| `README.md` | 如需同步状态，更新 T10 进度说明 |
| `REQUIREMENTS_ANALYSIS.md` | 如需同步状态，更新 T10 现状说明 |

---

## 5. 验证清单

- `go test ./...`（或至少相关包测试）通过
- 过滤规则新增/修改/删除后，mail-node 能收到 reload 通知
- 仪表盘显示今日创建数且数字合理
- 邮件正文查询对历史 `message_id` 命中正常
- 文档能独立指导一次部署、证书更新和 reload

---

## 6. 风险与回滚

| 风险 | 影响 | 缓解 |
|------|------|------|
| 主动推送失败影响 CRUD | 低 | 通知失败仅记录日志，不阻断写库 |
| 今日创建数统计口径偏差 | 低 | 先按 local today 聚合，口径保持简单 |
| message_id 兼容逻辑误命中 | 中 | 仅在查询侧做规范化，保持命中顺序明确 |
| TLS 文档与实际部署不一致 | 中 | 文档写完后用一次真实部署验证收口 |

---

## 7. 版本记录

| 日期 | 变更 |
|------|------|
| 2026-06-30 | v0.1 初版：T10 收尾四项能力拆分，排除临时机清理 |
