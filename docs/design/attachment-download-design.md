# 邮件附件下载设计

> 版本: v1.0 | 日期: 2026-07-02 | 状态: 已确认，实施中

> 衔接：邮件查询主线见 `docs/design/t8-mime-preprocessing-design.md`；管理后台邮件页见 `mgmt-system/template/admin/emails.html`。本设计补齐 t8 之后遗留的「附件只见元数据、拿不到内容」缺口。

---

## 1. 背景与目标

### 1.1 问题

`mail-node/internal/handler/message_parser.go` 的 `attachmentFromPart` 解析附件时只取 `len(part.Content)` 作为 size，**丢弃了字节内容**；全仓没有任何附件下载端点。邮件列表 / 正文接口返回的 `attachments[]` 仅含元数据（`filename / content_type / size / disposition / content_id / inline`）。

对国际订单邮箱场景，附件（行程单、发票、报关单 PDF 等）往往正是大模型系统与运营人员需要的核心数据——拿不到内容是功能性缺口。

### 1.2 目标

补齐「按 `index` 下载单个附件」的端到端能力：
- mail-node 新增返回原始字节的 `/internal` 端点
- mgmt-system 新增透传字节的代理路由（对外 API + 管理后台各一条）
- 前端详情页加下载入口

### 1.3 决策

| 项 | 选择 | 说明 |
|----|------|------|
| 传输方式 | **二进制流透传** | 端点直接返回原始字节，带真实 `Content-Type`；普通附件返回 `Content-Disposition: attachment`，inline MIME part 返回 `Content-Disposition: inline`；错误仍返回 JSON 信封。作为统一 JSON 信封约定的**例外**（文件下载/邮件内嵌资源业界标准） |
| 定位方式 | 按 `index` | 与列表/正文返回的 `attachment.index` 字段对齐，稳定无歧义 |
| 覆盖范围 | 普通附件 + inline part | `collectAttachmentParts()` 顺序固定为普通附件先于 inline part；正文 HTML 的 `cid:` 图片可按 `content_id -> index` 映射到该下载端点 |
| 范围 | 仅单个附件/inline 资源 | 不含整封 `.eml` 下载、不含附件 OSS 化、不含查询性能优化 |

### 1.4 与原需求的差异

`REQUIREMENTS_ANALYSIS.md §1.3` 原设想大模型系统拉取「text/plain + 附件 OSS URL」。当前裸机部署（2C2G，无对象存储），引入 OSS 成本高。本方案**直接从 mail-node 流式返回字节**，零额外存储依赖，契合部署约束；附件 OSS URL 作为后续可选演进保留。

---

## 2. 端到端链路

```
emails.html <a download> / HTML 预览 <img src="同源附件URL">
   │  浏览器同源自动带 mgmt_session cookie
   ▼
mgmt-system  proxyAttachmentToServer()  ← 不经 JSON 信封，保留响应头
   │  X-Internal-Token header
   ▼
mail-node  GET /internal/messages/:message_id/attachments/:index?mailbox=
           enmime 解析 .eml → collectAttachmentParts() → parts[index].Content → 字节流
```

**index 一致性约定**：列表 `parsedMessage.Attachments[i].Index`、正文同字段、下载端点取的 `parts[i]` 三处必须完全对齐。实现上让 `collectAttachments`（元数据）与下载端点共用同一个 `collectAttachmentParts`（返回 `[]*enmime.Part`，顺序：先 `envelope.Attachments` 后 `envelope.Inlines`），单一顺序来源杜绝错位。

---

## 3. 端点契约

### 3.1 mail-node 内部端点

`GET /internal/messages/:message_id/attachments/:index?mailbox=<email>`

- 鉴权：`X-Internal-Token`（与现有 `/internal/*` 一致，`mail-node/internal/middleware/auth.go`）
- `:message_id` 匹配沿用 `GetMessageBody` 的三级兼容（精确 / normalize 去 `<>` / fallback-id 忽略大小写）
- 响应：
  - 成功 `200`：`Content-Type` = `part.ContentType`（空则 `application/octet-stream`）；普通附件 `Content-Disposition: attachment; filename="<ascii fallback>"; filename*=UTF-8''<urlencoded 原名>`；inline MIME part `Content-Disposition: inline; ...`（同样保留 RFC 5987 文件名编码）；body = 附件/inline 资源原始字节
  - `400` `{code:1002}`：mailbox 非法 / index 非数字
  - `404` `{code:2003}`：邮件未找到 / index 越界
  - `500` `{code:5000}`：解析失败

### 3.2 mgmt-system 对外 API

`GET /api/v1/emails/:message_id/attachments/:index?mailbox=<email>`

- 鉴权：`Authorization: Bearer <token>` + scope `email:read`（挂 `emailGroup`，`main.go:125`）
- 行为：查 `mailbox_accounts` 定位服务器 → 透传字节流
- 响应：透传 mail-node 的状态码、`Content-Type`、`Content-Disposition`、body

### 3.3 mgmt-system 管理后台 API

`GET /api/v1/admin/emails/:message_id/attachments/:index?mailbox=<email>`

- 鉴权：session cookie `mgmt_session`（`AdminAuthRequired`，`main.go:108`）
- 行为：复用 `AdminGetEmailBody` 的**域名级降级查找**——mailbox 不在 `mailbox_accounts` 时按域名定位服务器（`FindServerByEmailDomain`），让管理员能下载任意账号的附件
- 响应：同 3.2

---

## 4. 安全

- 三条端点全部复用现有鉴权链（Bearer+scope / session / internal-token），**无新增中间件、无 URL 内嵌 token**。
- 前端 `<a download>` 同源请求自动携带 session cookie，零额外鉴权代码；HTML 预览中的 inline 图片 URL 同样走同源 Session 保护接口。
- HTML 预览只将 `cid:` 引用重写到本系统附件 URL，默认不加载外部远程图片，避免追踪像素和隐私泄露。
- 文件名经 `url.PathEscape` 编码后写入 `Content-Disposition`，避免响应头注入。
- 附件内容来自已落地的 Maildir 文件，无外部输入直接拼路径（路径由 email → `domain/local` 拼接，email 已做格式校验）。

---

## 5. 已知限制（本次不解决）

- **大附件内存**：enmime 全量解析 + `part.Content` 常驻内存，mgmt 侧虽 `io.Copy` 流式但仍经 mail-node 全量读。单附件超大（>几十 MB）时双倍内存压力；订单邮件附件通常不大，可接受。后续可优化为按 MIME part 偏移流式读取。
- **错误透传体验**：下载失败时浏览器可能保存一个含错误 JSON 的小文件（HTTP 语义如此），调用方需据状态码判断。
- **不含查询性能项**：`GetMessageBody` 全量遍历解析、列表全扫——作为独立后续任务（见查询完善度评估）。
- **不含整封 `.eml` 下载 / 附件 OSS 化**：后续可选演进。

---

## 6. 验证

1. `cd mail-node && go build ./... && go test ./...`
2. `cd mgmt-system && go build ./... && go test ./...`
3. 管理后台端到端：带附件邮件 → `/admin/emails` 选邮件 → 详情页点附件「下载」→ 校验字节一致（含中文文件名）。
4. 对外 API：`curl -H "Authorization: Bearer <token>" ".../emails/<id>/attachments/0?mailbox=..." -o out.bin -D h.txt`，检查 `Content-Type` / `Content-Disposition` / 字节 / 状态码。
5. 错误路径：越界 index → 404；不存在 message_id → 404；未鉴权 → 401。
