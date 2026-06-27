# mgmt-system 控制面缺口定版（Phase 3）

> 版本: v0.4 | 日期: 2026-06-26 | 状态: **T4/T5/T6/T7 已闭环，mgmt 国际机已部署；下一批 T8/T9/T10**
> 依据: `REQUIREMENTS_ANALYSIS.md` §2.1 / §2.4 / §4 + context.md 最新状态

---

## 1. 背景与目标

Phase 2 已完成**数据面闭环**：国际机收发信 + mail-node 自动转发 union + Roundcube 全部上线验证。但**控制面 mgmt-system 的功能完成度参差**——与 mail-node 的被动集成链路（创建/查询透传、心跳接收、filter 拉取）是通的，缺的是 mgmt 侧的「控制智能」。

**Phase 3 目标**：补齐 mgmt 控制面，使整套系统达到「可上线、可服务大模型系统」的状态。

> 本稿是**规划评审稿**，只对齐「做什么 / 关键取舍 / 顺序」，通过后每个子项再出详细设计。

---

## 2. 缺口定版

| # | 功能（需求条目） | 现状 | 目标 | 优先级 |
|---|------------------|------|------|--------|
| C1 | 邮箱账号集合管理（§2.1.1/§2.1.6） | ✅ T1/T2/T3 已完成 | 账号台账含账密/域名/服务器/状态，支持筛选 | ✅ 已完成 |
| C2 | 邮件 MIME 预处理（§2.4/§2.2.4） | 裸透传 mail-node 原始响应 | 结构化 `{subject,body,attachments}` | 🔴 P0 → T8 |
| C3 | 服务器健康检查（§2.1.3） | ✅ T7 已完成 | 主动探测 + 超时降级 + 自动摘除 | ✅ 已完成 |
| C4 | 域名感知负载分配（§2.1.3） | ✅ T4/T5 已完成 | server_domains + 按域名筛 + 最闲分配 | ✅ 已完成 |
| C5 | 邮箱生命周期状态机（§2.1.5） | 3 态，无 GC，DELETE 不下发 | 四态 + 定时 GC + 重新启用 | 🟡 P1 → T9 |
| C6 | 鉴权体系（§4.4） | ✅ T6 已完成 | 后台 session + Bearer Scope + Shared-Secret | ✅ 已完成 |
| C7 | 域名管理（§2.1.6） | ✅ T4/T5 已完成 | 服务器域名池 + Postfix/DKIM + DNS 清单 | ✅ 已完成 |
| C8 | 批量幂等 / 缺失页面 / 停用下发 等 | 部分修复 | filter 主动推送、停用下发等收尾 | 🟢 P2 → T10 |
| E1 | 订单-邮箱 N:M 映射扩展（§2.1.2） | 暂缓 | 后续按扩展方案评审 | 🟢 扩展 |
| O1 | mgmt 部署国际机 | ✅ 已完成（2026-06-25） | MariaDB + Nginx + systemd | ✅ 已完成 |
| O2 | inet_protocols / 临时机清理 / Let's Encrypt | — | 运维收尾 | 🟢 → T10 |

**优先级结论**：
- **近期只做 mgmt-system 控制面**。mail-node 数据面主链路已基本完整，不作为 Phase 3 主短板。
- **第一批必须先打地基**：C1 邮箱账号集合管理、创建链路一致性、C7 域名管理基础、C4 域名感知分配的最小模型。
- **C2 MIME 预处理很重要，但可以在账号台账/创建链路一致性之后做**，避免先在不可信账号数据上搭 API。
- **E1 订单-邮箱 N:M 明确暂缓**，不得在当前阶段引入 schema/API 复杂度。

---

## 3. MVP 代码现实状态（2026-06-26 更新）

以下为 Phase 3 启动时的代码审读结论。**标记 ✅ 的项已在 T1–T7 中修复。**

| 模块 | 代码现状 | 状态 |
|------|----------|------|
| 邮箱账号模型 | T1 已补 `password/sync_status/sync_error/synced_at`，新账号密码可持久化 | ✅ |
| 邮箱列表 | T3 已完成：账号台账 preload Domain/Server，支持 `domain_id/server_id/status/search` 筛选 | ✅ |
| 外部创建 API | T2 已完成：`MailboxCreator` 先远端成功后落本地，远端失败不落 DB | ✅ |
| 后台批量创建 | T2 已统一走 `MailboxCreator`，创建链路一致性已收敛 | ✅ |
| 幂等语义 | T2 已修复：按 `order_id` 或 `email_address` 幂等返回已有账号 | ✅ |
| 域名管理 | T4/T5 已完成：`server_domains` 表 + 域名池 CRUD + Postfix 虚拟域 + DKIM + DNS 清单 | ✅ |
| 鉴权 | T6 已完成：Session 登录 + Bearer Token Scope + Shared-Secret 内部互信，已部署国际机 | ✅ |
| Scope | T6 已修复：`RequireScope` 类型断言改为 `*model.ApiToken`，缺失/错误 scope 正确返回 403 | ✅ |
| 邮件查询 | 调整为按邮箱维度查询；T8 MIME 结构化预处理为当前待实施项 | 🔜 |
| 生命周期 | 四态/停收/软删/GC 流程待 T9 对接 | 🔜 |
| 健康检查 | T7 已完成：主动探测 + 心跳 + 降级摘除 + DB 落库 + 仪表盘可观测 | ✅ |

---

## 4. 设计决策（需评审）

### 4.1 邮箱账号集合管理（C1）⭐ 运营控制台基础能力

**需求定位**：这不是订单-邮箱 N:M 映射，而是 mgmt 对“已经注册/创建到系统中的邮箱账号资产”的可视化管理。运营需要能直接看到账号集合，包含账密，并能按邮箱绑定域名筛选。

**必要模型修正**：
- `OrderMailbox` 增加 `password`（或独立凭据表）用于保存 Dovecot 登录密码；当前阶段可明文保存以满足运营/联调，生产环境再评估加密存储。
- 列表查询 preload `Domain` / `Server`，支持 `domain_id`、`server_id`、`status`、关键词搜索。
- 批量/单个/外部 API 创建路径都必须写入同一套账号字段，避免“有的路径有密码、有的路径没有”。

**目标页面**：增强现有邮箱账号管理页，形成账号台账。

**字段建议**：
- 邮箱地址、密码、绑定域名、所在服务器、状态、创建时间、更新时间
- 可选展示：最后心跳/最后同步状态、当前邮件数量、备注

**筛选与操作**：
- 按绑定域名筛选（P0）
- 预留按服务器、状态、关键词搜索
- 密码展示/复制：当前阶段可明文展示，生产上线必须放在后台鉴权之后，后续可加脱敏开关和操作审计
- 账号停用/启用/回收入口沿用生命周期设计，实际下发见 C5

**实现边界**：
- 只展示 mgmt 已知并登记的邮箱账号，不直接 SSH 读取远端 `users.conf`
- 如发现 mgmt DB 与 mail-node 实际账号不一致，后续可加“同步检查/重拉”动作；当前主线先做好 mgmt 台账
- 远端创建失败的处理策略必须收敛：要么失败不落本地，要么标记为 `sync_failed` 并提供重试；不能静默当成功

**工作量**：小～中（列表字段补齐 + 域名筛选 + 前端表格/复制交互）

---

### 4.2 订单-邮箱 N:M 映射扩展（E1，暂缓）

**当前决策（2026-06-24）**：N:M 映射先作为扩展方案保留，Phase 3 主线暂不实施。原因是它会改变邮件查询 API 的业务语义，并带来 schema、创建流程、查询流程、前端页面的一揽子重构；当前优先补齐 mgmt 控制面上线能力。

**现状根因**：`OrderMailbox.OrderID` 带唯一约束，数据库层偏 1:1；无独立 mappings 表。

**扩展方案草案**：
- 新建 `order_mailbox_mappings` 表：`(id, order_id VARCHAR(128), mailbox_account_id UINT64 FK→mailbox_accounts, created_at)`，`UNIQUE(order_id, mailbox_account_id)`，`INDEX(mailbox_account_id)`、`INDEX(order_id)`
- `order_mailboxes` 表退化为**纯邮箱实体**（status / retention / server_id / domain_id），**移除 order_id 字段**（或保留为「创建时初始订单」兼容字段，最终移除）
- 创建流程改造：**邮箱创建与订单绑定解耦**——`POST /mailboxes` 只建邮箱（可带可选 `order_id` 顺带绑一个）；新增 `POST /mappings` 绑定、`DELETE /mappings` 解绑、`GET /mappings?order_id=` / `?mailbox_id=` 双向查询
- 现有 1:1 数据迁移脚本：把 `order_mailboxes.order_id` 既有值灌入 mappings 表

**暂缓前必须确认的语义后果**（N:M 的固有特性）：
> 共享邮箱意味着——**按订单查邮件时，会返回该邮箱收到的全部邮件，其中可能含其他订单触发的邮件**。如果业务要求「按订单严格隔离邮件内容」，则 N:M 模型不成立，需回退 1:1。
>
> 只有业务明确接受这个语义后，才进入详细设计和实现。

**当前主线**：保持现有 1:1/按邮箱查询模型，不做 N:M schema 重构。

**扩展工作量**：大（动 schema + 创建/查询全链路 + 前端映射页 + 迁移）

---

### 4.3 邮件 MIME 预处理（C2）⭐ 大模型数据供给

**现状**：`email.go` 把 mail-node 的原始响应直接转发，无 MIME 解析。大模型系统拿不到干净的 text/附件结构。

**推荐方案**：
- **在 mgmt 侧做**（不在 mail-node）：mgmt 作为大模型对接点，负责数据加工；mail-node 保持原始透传
- 引入 `github.com/jhillyerd/enmime`（比标准库 `mime/multipart` 强很多，处理嵌套 multipart、乱码 subject、附件提取）
- 返回结构：
  ```json
  {
    "message_id": "...", "from": "...", "to": "...", "subject": "(已解码)",
    "date": "...",
    "text_body": "(text/plain 优先，否则从 html 提取)",
    "html_body": "(可选)",
    "attachments": [
      {"filename":"...", "content_type":"...", "size":N, "download_url":"/api/v1/emails/{id}/attachments/{idx}"}
    ]
  }
  ```
- **附件不内联 base64**（大文件爆响应），返回元数据 + 分离下载端点（需 Token）
- subject 用 enmime 自动 RFC2047 解码（解决 `=?utf-8?B?...?=` 乱码）

**待定**：邮件原文（raw .eml）是否也提供一个下载端点？推荐提供（大模型可能要原始件）。

**工作量**：中（单个 handler 重写 + 附件下载端点 + 单测）

---

### 4.4 服务器健康检查 + 状态机（C3）

**推荐方案**：
- mgmt 起**主动探测 goroutine**：每 30s `GET mail-node /internal/health`，超时 5s
- 三态判定（与被动心跳融合）：连续 3 次探测失败 → `degraded`；连续 5 次 → `down`
- `down` / `draining` 不参与邮箱分配（`store.go` 分配查询加状态过滤）
- 心跳超时兜底：`last_heartbeat` 超 90s 未更新且探测失败 → down
- 仪表盘/服务器页展示实时状态

**工作量**：中（1 个 scheduler 模块 + store 查询调整）

---

### 4.5 域名感知负载分配（C4）

**推荐方案**：
- 新建 `server_domains` 表：`(server_id, domain_id)`，M:N
- 服务器注册/编辑页加「可服务域名」多选
- 分配算法：`WHERE domain_id=? AND status=healthy ORDER BY current_load ASC LIMIT 1`
- 创建邮箱时，按目标域名筛可用服务器池

**依赖**：C7 域名管理（至少要有域名 CRUD）。

**工作量**：中（新表 + 分配器改造 + 前端）

---

### 4.6 邮箱生命周期状态机 + GC（C5）

**推荐方案**：
- 四态：`active → disabled → soft_deleted → purged`（`disabled` 即停用）
- 停用 API 改为：本地置 `disabled` + **下发 mail-node** 摘除 Postfix map（停收新信，保留历史）
- **定时 GC goroutine**：扫 `disabled` 且 `now - disabled_at > retention_days` 的 → 调 mail-node `MoveToTrash`（软删）→ 置 `soft_deleted`；再过宽限期 → `purged`
- 重新启用 API：`soft_deleted` 内可恢复
- mail-node 的 `Lifecycle.MoveToTrash` 已实现（forward 模块），mgmt 只需对接调用

**工作量**：中（状态机 + GC scheduler + 对接 mail-node）

---

### 4.7 鉴权体系（C6）

**推荐方案**：
- **管理后台**：session 登录（用户名/密码，cookie session），运营人员用——不是 API Token。`/admin/login` 页 + session 中间件
- **对外 API**（`/api/v1/mailboxes`、`/api/v1/emails`）：保留 Bearer Token，接线 `RequireScope`（出票中心用 `mailbox:write`，大模型用 `email:read`）
- **内部接口**（`/api/v1/internal/*`）：加 shared-secret header 校验（mgmt ↔ mail-node 互信）
- **前置依赖**：mgmt 部署国际机 + Nginx 在前（生产部署 `/admin` 须 IP 白名单/Basic Auth 双保险）

**工作量**：中（session 机制 + scope 接线 + 内部 secret）

---

### 4.8 域名管理（C7）

**推荐方案**：
- 域名 CRUD API（`/api/v1/admin/domains`）+ 管理后台域名页
- MX 状态检测：mgmt 用 `net.LookupMX` 查域名 MX，与配置比对，页面展示「MX 配置是否正确」
- 关联 C4 的 `server_domains` 绑定

**工作量**：小～中（CRUD + 页面 + DNS 查询）

---

### 4.9 其他 P2（C8）

| 项 | 方案 |
|----|------|
| 批量创建不幂等 | 已存在邮箱返回已有地址而非 fail；去掉「prefix 冒充 order_id」 |
| 停用不下发 | 见 C5，停用同步摘除 mail-node |
| 缺失页面 | 域名管理页（C7）、邮件查询调试页（C2）；订单映射页作为 E1 扩展方案暂缓 |
| 过滤规则主动推送 | mgmt 规则变更后 `POST mail-node /internal/filters/reload`（被动拉取保留） |
| 仪表盘「今日创建数」 | 加一个 count 查询 |
| `parseFullMessage` message_id 尖括号 | URL 编码或服务端 strip（小 bug） |

---

## 5. 分阶段路线图

```
Phase 3A — 地基（前置，无此则线上功能跑不起来）
  O1  mgmt 部署国际机（MySQL + binary + Nginx）
  C6  鉴权体系（session + scope + 内部 secret）   ← 部署完顺手做

Phase 3B — 控制面基础能力
  C1  邮箱账号集合管理（账密展示 + 域名筛选）
  C7  域名管理（C4 依赖）

Phase 3C — 控制面智能
  C3  健康检查 + 状态机
  C4  域名感知分配
  C5  生命周期 + GC

Phase 3D — 大模型数据供给
  C2  MIME 预处理 ⭐

Phase 3E — 收尾
  C8  批量幂等 / 缺失页面 / 主动推送 / 仪表盘
  O2  运维收尾（ipv4 / 临时机清理 / Let's Encrypt）

Phase 3X — 扩展方案（暂缓）
  E1  订单-邮箱 N:M 映射（待业务确认共享邮箱语义）
```

**依赖关系**：O1→C6；C1 依赖现有邮箱表与域名字段，和 C7 可并行；C7→C4；E1 暂缓，不阻塞 C2；C2 先按邮箱维度/现有查询语义提供结构化邮件。

---

## 6. 续接执行清单（可直接开工）

### Sprint 1：账号台账与创建链路收敛（最高优先级）

**T1. 数据模型补齐（已完成，2026-06-24）**
- `OrderMailbox` 增加 `password` 字段；当前阶段允许明文保存。
- 增加同步状态字段，建议：`sync_status`（`synced` / `sync_failed` / `pending`）、`sync_error`、`synced_at`。
- 保留现有 `order_id` 字段兼容 MVP，不做 N:M 拆表。
- AutoMigrate 后注意已有数据 password 为空的历史账号，需要页面显示为“未记录”。

**验收**：✅ 已通过。新创建邮箱刷新页面后仍能看到密码；历史邮箱不会因空密码崩溃。验证账号：`t1-check-0624-01@example.com` / `<password>`，`sync_status=synced`。

**T2. 统一创建服务（已完成，2026-06-24）**
- 抽出统一创建流程：生成/接收密码 → 选域名 → 选服务器 → 调 mail-node `/internal/mailboxes` → 成功后写 mgmt DB。
- 外部 `POST /api/v1/mailboxes` 和后台 batch/upload 共用同一套逻辑。
- 远端失败策略：MVP 采用“远端失败则不写本地，返回失败”，避免污染账号台账。
- 外部 API 按 `order_id` 幂等返回已有记录；后台批量/CSV 复用同一 `MailboxCreator` 流程。

**验收**：✅ 已通过。本地 `test-node-01`（127.0.0.1:8081）不可用时创建失败且不落本地；将该节点标记为 `down` 后，自动分配到国际机 `mail-node-intl`（server_id=2）。验证账号：`t2-api-0624-01@example.com`（外部 API，幂等重试返回 `already_exists`）、`t2-batch-0624-01@example.com`（后台批量，密码 `<password>`），均为 `sync_status=synced`。

**T3. 邮箱账号集合页（已完成，2026-06-24）**
- 账号台账从 `order_mailboxes` 拆出为 `mailbox_accounts`，维度为 server + domain + mailbox + credential；订单关系由 `order_mailbox_mappings` 绑定账号 ID。
- 列表 preload `Domain` / `Server`，展示域名名和服务器名，不再只显示 ID。
- 显示邮箱地址、密码、域名、服务器、状态、同步状态、创建时间。
- 支持按 `domain_id`、`server_id`、`status`、关键词搜索；后台页面第一屏为“邮箱账号台账”。
- 当前阶段明文展示密码；复制按钮尚未做，留到 T10/UX 收尾。

**验收**：✅ 已通过。`/admin/mailboxes?domain_id=1` 返回 200，页面包含“邮箱账号台账”“密码”“example.com”“mail-node-intl”；`/api/v1/admin/mailboxes?domain_id=1&server_id=2` 返回 `mailbox_accounts` 台账。启动时已通过 SSH 从国际机 `mail-node-intl` 的 `/etc/dovecot/users.conf` 导入真实账号 7 个，包括 `union@example.com / <password>`、`dns-test@example.com / <password>`、`test-fix@example.com / <password>`、`intl-test@example.com / <password>` 以及 T1/T2 验证账号。

### Sprint 2：域名与服务器分配

**详细设计事实来源**：`docs/design/t4-t5-server-domain-pool-design.md`。本计划只保留摘要；T4/T5 的模型、接口、状态语义、执行拆分和验证清单均以该文档为准。

**T4/T5 合并方向：服务器域名池（宝塔式）**

> 2026-06-24 状态更新：T4A/T4B/T5A/T5B/T5C 已完成并验证。服务器域名池、域名感知分配、mail-node Postfix 虚拟域、DKIM 自动生成、移除域名保护/清理、DNS A/MX/SPF/DKIM/DMARC 清单展示均已闭环。添加域名 UI 已改为“原始域名 + A 记录主机头”，默认 `mail`；mgmt 生成 `A <host>.<domain> -> 服务器 IP` 和 `MX <domain> -> <host>.<domain>`，DKIM 仍挂在 `<selector>._domainkey.<domain>`。

- 不再把 T4 做成独立的“全局域名 CRUD”主线；主入口改为服务器视角的「域名池」。
- `server_domains` 是 server + domain 的 M:N 绑定表，并记录远端同步状态：`sync_status`、`postfix_status`、`dkim_status`、`sync_error`、`dkim_selector`、`dkim_public_key`、`synced_at`。
- 分配器只能使用 `status=active` 且 `postfix_status=synced` 的服务器-域名绑定；DKIM 失败可进入 `partial`，但 UI 必须提示“收信可用、发信签名待修复”。
- 邮箱创建入口优先落在「服务器 → 域名」上下文，固定 `server_id + domain_id`；外部 API 可选传 `domain_id`，由域名感知分配选择健康服务器。

**执行拆分**
- **T4A**：mgmt 域名池地基。建 `ServerDomain`/`server_domains`，补 store 查询与域名感知分配方法，把当前 `mail-node-intl(server_id=2) + example.com(domain_id=1)` seed 为 synced，只读展示服务器域名池。
- **T4B**：域名池下创建邮箱。域名池页支持单个/批量创建，`MailboxCreator` 校验指定 server 是否已绑定指定 domain 且 `postfix_status=synced`。
- **T5A**：mail-node Postfix 虚拟域数据面。新增 `/internal/domains` Add/List/Remove，真正维护 `virtual_mailbox_domains`。
- **T5B**：DKIM 与 DNS 记录。每台服务器使用独立 selector（如 `mail-s2`），返回 MX/SPF/DKIM/DMARC 记录，并保存 DKIM 状态到 `server_domains`。
- **T5C**：移除域名与回归。有邮箱的 server-domain 绑定不得移除；无邮箱测试域可远端清理并本地 inactive。

**验收摘要**：服务器页能看到国际机 `example.com` 域名池；该域名池下建邮箱会真实落国际机；外部 `POST /api/v1/mailboxes {domain_id}` 只分配到绑定该域的健康服务器；新增测试域能在 mail-node 侧写入 Postfix 虚拟域并返回 DNS/DKIM 记录。

### Sprint 3：安全与运行状态

**T6. 鉴权**
- 后台加 session 登录。
- `/api/v1/admin/*` 不再裸放行。
- 对外 API 接入 `RequireScope`，先修正其类型断言。
- `/api/v1/internal/*` 与 mail-node `/internal/*` 加 shared-secret。

**验收**：无登录不可访问后台；无正确 token/secret 不可调用 API/internal。

**T7. 健康检查与 node 心跳**
- mgmt 增加主动探测 scheduler：定时 `GET mail-node /internal/health`。
- node `startHeartbeat` 改为真实 POST mgmt heartbeat。
- mgmt 消费 `last_heartbeat`，超时降级/down；down/draining 不参与分配。

**验收**：停掉 mail-node 后 mgmt 能自动把服务器标为 degraded/down，并停止分配新邮箱。

### Sprint 4：邮件数据供给

**T8. MIME 预处理**
- mgmt 引入 MIME 解析库，优先 `text/plain`，必要时从 HTML 提取文本。
- 附件返回元数据，不内联 base64。
- API 先按邮箱维度查询：`GET /api/v1/mailboxes/{email}/emails` 或等价方案；订单维度暂缓。

**验收**：大模型系统拿到结构化 `{subject, from, date, text_body, attachments}`。

### Sprint 5：生命周期与收尾

**T9. 生命周期对接**
- mgmt 四态：active/disabled/soft_deleted/purged（或兼容旧状态迁移）。
- 停用/删除调用 mail-node 删除协议。
- 实现 `/api/v1/internal/sync/deleting`，供 node 重启对账。

**验收**：停用后 Postfix/Dovecot 摘除，历史邮件软删/恢复/清理流程可追踪。

**T10. 低优先级收尾**
- 过滤规则主动推送 reload。
- 仪表盘今日创建数。
- `message_id` 尖括号/URL 编码兼容。
- 运维：mgmt 部署国际机、`inet_protocols=ipv4`、Let's Encrypt、临时机清理。

---

## 7. 待用户决策清单（评审重点）

| # | 决策点 | 推荐 | 影响 |
|---|--------|------|------|
| D-1 | 邮箱账号集合是否展示明文密码？ | 是，先满足运营/联调；后台鉴权后再做脱敏/审计增强 | C1/C6 |
| D-2 | MIME 预处理放 mgmt 还是 mail-node？ | mgmt（对接点在此） | C2 |
| D-3 | 附件：元数据+下载URL 还是 内联 base64？ | 元数据+URL | C2 |
| D-4 | 后台鉴权：session 还是 Token？ | session（运营人员） | C6 |
| D-5 | 健康检查参数：探测 30s / 降级 3 次 / down 5 次，可否？ | 如左 | C3 |
| D-6 | 推进顺序是否按 3A→3E？ | 是 | 全局 |
| D-7 | 订单-邮箱 N:M 是否进入实现？ | 暂缓，作为扩展方案 | E1 |

---

## 附录：工作量粗估（人天，含测试）

| 子项 | 估计 |
|------|------|
| O1 部署 | 1 |
| C6 鉴权 | 2 |
| C1 邮箱账号集合管理 | 1～1.5 |
| C7 域名管理 | 1.5 |
| C3 健康检查 | 2 |
| C4 域名分配 | 1.5 |
| C5 生命周期 | 2.5 |
| C2 MIME 预处理 | 3 |
| C8 收尾杂项 | 2 |
| **合计（不含 E1）** | **≈16～17 人天** |
| E1 N:M 扩展（暂缓） | 4～5 |

> 粗估，详细设计阶段细化。当前主线里 MIME（C2）是最重的点；N:M（E1）暂不计入 Phase 3 主线。
