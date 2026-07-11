# 邮箱创建入口合并与域名选择 — 设计文档

> 版本: v0.3 | 状态: 已实现并发布 | 最后校准：2026-07-11
> 依据: 用户确认需求（邮箱管理统一创建入口、服务器/域名可选且联动、批量 CSV 账密下载）
> 关联: `docs/design/t4-t5-server-domain-pool-design.md`、`docs/design/integrated-mailbox-design.md`
> 2026-07-08 更新：底部固定创建区体验不佳，第二次优化改为「创建邮箱」作为邮箱页 tab，排在「集成邮箱」之后。

---

## 1. 背景与目标

当前“创建邮箱”能力存在两个入口：

- 域名池页面：在服务器/域名上下文里创建邮箱。
- 邮箱管理页面：已有创建入口，但入口与账号管理 tabs 混在一起，创建结果展示不稳定，批量结果缺少账密下载。

本次调整目标是把创建邮箱能力统一收口到“邮箱管理”页面，域名池只负责服务器域名绑定、Postfix/DKIM 同步与 DNS 管理。邮箱管理页以 tab 方式展示账号集合、回收站、集成邮箱和创建邮箱；创建邮箱排在集成邮箱后面，支持单个/批量创建，并提供可选服务器、可选域名的互相联动选择。

---

## 2. 需求拆分

| 编号 | 需求 | 说明 |
|------|------|------|
| R1 | 创建入口合并 | 创建邮箱只在邮箱管理页展示，作为 tab 排在「集成邮箱」后面 |
| R2 | 域名池去创建化 | 域名池页面不再作为创建邮箱入口 |
| R3 | 单个创建选择服务器/域名 | 服务器和域名均可选，不选则自动分配 |
| R4 | 批量创建选择服务器/域名 | 表单级服务器/域名作为批量行默认创建上下文 |
| R5 | 服务器/域名互相联动 | 选服务器限制域名；选域名限制服务器；后端做最终校验 |
| R6 | 自动分配提示 | 未选择时提示按健康服务器与可用域名池自动/负载随机创建 |
| R7 | 单个创建结果 | 创建成功后立即展示邮箱地址和密码 |
| R8 | 批量账密下载 | 批量创建后提供 CSV 下载，包含成功账密和失败原因 |
| R9 | 后台同名创建失败 | 邮箱管理页手动创建遇到同 prefix/order_id 或同 email_address 时返回失败；外部订单 API 保持幂等 |

---

## 3. 设计决策

| 决策 | 状态 | 说明 |
|------|------|------|
| 创建区域位置 | 已调整 | v0.1 曾放在页面最后；v0.2 改为 tab「创建邮箱」，排在「集成邮箱」之后，避免底部大表单破坏页面观感 |
| 服务器/域名关系 | 已确认 | 前端互相联动，后端权威校验，防止非法绑定落库 |
| 批量账密格式 | 已确认 | CSV，便于 Excel 打开和后续导入 |
| API 兼容性 | 已确认 | 优先复用现有 JSON batch create API，不破坏已有调用方 |
| 自动分配范围 | 已确认 | 只从 active domain + healthy server + active/synced server_domain 中选择 |
| 重复创建语义 | 已确认 | 外部订单 API 保持 `order_id` 幂等；后台手动创建重复 prefix/email 明确失败 |

---

## 4. 前端方案

### 4.1 邮箱管理页结构

修改 `mgmt-system/web/src/pages/MailboxesPage.jsx`：

- 保留页面顶部“创建邮箱”主按钮，但行为改为切换到 `view='create'`，不再滚动到页面底部。
- tabs 顺序：账号集合、回收站、集成邮箱、创建邮箱。
- 创建表单仅在 `view === 'create'` 时渲染，作为独立 section 占据当前 tab 内容。
- 创建结果留在创建 tab 内展示，切换到账号集合不会意外丢失结果；成功创建后可提示“查看账号集合”。
- 移除页面底部常驻创建 section，降低邮箱管理页整体噪声。

### 4.2 创建表单

单个与批量创建共用以下表单级上下文：

- 邮箱服务器：可为空。
- 域名：可为空。
- 自动分配提示：当两者都为空时显示。

选择规则：

- 选择服务器后，域名下拉只显示该服务器已绑定且可用的域名。
- 选择域名后，服务器下拉只显示服务该域名的可用服务器。
- 若已有选择变为不合法，前端自动清空另一侧或提示重新选择。
- 前端过滤只改善体验；后端仍校验服务器健康、域名 active、server_domain active、postfix_status=synced。

### 4.3 批量输入

继续兼容当前文本格式：

```text
prefix,password
prefix2,
```

表单级服务器/域名会被附加到每个 batch item：

```json
{
  "prefix": "order-001",
  "password": "optional",
  "server_id": 1,
  "domain_id": 2
}
```

### 4.4 结果展示与 CSV 下载

单个创建：

- 展示邮箱地址、密码、同步状态、服务器 ID、域名。
- 提供复制按钮。
- 可下载单行 CSV。

批量创建：

- 展示 total/success/failed。
- 展示每行 status、email_address、password、error。
- 提供 CSV 下载。

CSV 字段：

```csv
email_address,password,prefix,domain,server_id,status,error
```

---

## 5. 后端方案

### 5.1 创建 API

复用 `mgmt-system/internal/handler/mailbox.go`：

- `BatchCreateItem` 已包含 `prefix/password/domain_id/server_id`。
- `CreateMailboxBatch` 继续返回 JSON summary。
- `processBatchCreate()` 继续复用 `MailboxCreator.Create()`。
- `BatchCreateResult` 补充必要的 `domain`、`server_id` 字段，便于前端展示和 CSV 下载。
- 后台批量创建通过 `MailboxCreateInput.AllowExisting=false` 执行，遇到已有 `order_id/prefix` 或已有 `email_address` 时返回失败行，不按幂等成功处理。
- 外部 `POST /api/v1/mailboxes` 仍通过 `Allocator.Allocate()` 设置 `AllowExisting=true`，保持订单创建接口的重试幂等语义。

### 5.2 自动域名选择

修改 `mgmt-system/internal/service/mailbox_creator.go`：

- 当前 `selectDomain(0)` 取第一个 active domain。
- 调整为只选择可实际投递的域名：该域名至少存在一个 active server_domain，且对应服务器 healthy、有容量，postfix_status=synced。
- 在 `mgmt-system/internal/store/store.go` 增加 helper，例如 `GetAllocatableDomain()`。

建议查询条件：

- `domains.status = active`
- `server_domains.status = active`
- `server_domains.postfix_status = synced`
- `mail_servers.status = healthy`
- `mail_servers.current_load < mail_servers.capacity`

### 5.3 服务器选择

保留现有 `selectServer(serverID, domainID)` 语义：

- 指定服务器时校验 healthy。
- 指定服务器和域名时校验 server_domain 存在且 active，并且 postfix_status=synced。
- 未指定服务器但指定域名时，复用 `GetHealthyServerForDomain(domainID)`。

---

## 6. 域名池页面调整

修改 `mgmt-system/template/admin/server_domains.html`：

- 移除或隐藏“单个创建/批量创建邮箱”控件。
- 保留域名池列表、同步状态、DNS 信息、邮箱数量统计。
- 如需引导，展示提示：创建邮箱请到“邮箱管理”页面的「创建邮箱」tab 完成。

---

## 7. 变更清单

| 文件 | 变更 |
|------|------|
| `docs/design/mailbox-creation-consolidation-design.md` | 本设计文档 |
| `mgmt-system/web/src/pages/MailboxesPage.jsx` | 创建区域改为「创建邮箱」tab，新增服务器/域名联动、结果展示、CSV 下载 |
| `mgmt-system/web/src/api.js` | 如需补充绑定元数据 API wrapper，则在此增加 |
| `mgmt-system/internal/service/mailbox_creator.go` | 修正未指定域名时的可投递域名选择 |
| `mgmt-system/internal/store/store.go` | 增加可分配域名查询 helper |
| `mgmt-system/internal/handler/mailbox.go` | 补充 batch result 字段，必要时扩展 CSV parse |
| `mgmt-system/template/admin/server_domains.html` | 清理域名池创建入口 |

---

## 8. 验证方案

### 8.1 后端

- 未选择 domain/server 时，只能选择可投递域名和健康服务器。
- 指定 domain 时，只能分配到服务该域名且 Postfix synced 的服务器。
- 指定 server + domain 不匹配时返回清晰错误。
- 批量创建结果包含邮箱地址、密码、状态和错误原因。
- 后台重复创建同 prefix/order_id 时返回失败行，不隐藏为幂等成功。
- 外部订单 API 重复 `order_id` 请求仍返回 `already_exists`/`is_existing=true`，保证调用方重试安全。

### 8.2 前端

- 邮箱管理页顶部保留“创建邮箱”主按钮，点击后切换到「创建邮箱」tab。
- tabs 包含「账号集合 / 回收站 / 集成邮箱 / 创建邮箱」，且「创建邮箱」排在「集成邮箱」后面。
- 创建区域只在「创建邮箱」tab 内出现，不再常驻页面底部。
- 选服务器后域名下拉收窄；选域名后服务器下拉收窄。
- 两者都不选时展示自动分配提示。
- 单个创建后账密可见。
- 批量创建后可下载 CSV，且生成密码写入文件。

### 8.3 回归

- 账号集合筛选、回收站、集成邮箱管理不受影响。
- 域名池页面仍可查看绑定、同步状态和邮箱数量。
- 现有 `POST /api/v1/admin/mailboxes/batch` JSON 调用保持兼容。
