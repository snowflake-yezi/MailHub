# 外部 API 对接文档

> 面向出票中心、大模型系统等外部调用方。所有接口统一由 `mgmt-system` 暴露，数据面 `mail-node` 仅通过内部 Shared-Secret 接口被控制面调用。

## 1. 基础约定

### 1.1 基础路径

```http
https://<mgmt-host>/api/v1
```

### 1.2 鉴权

外部 API 使用 Bearer Token：

```http
Authorization: Bearer <token>
```

Token 由管理端“外部访问”页面签发。管理员创建并命名外部应用、勾选可调用功能后获得一次性 Token；完整 Token 只展示一次，数据库只保存哈希。

- `mailbox:create`：创建或复用邮箱。
- `mailbox:read`：查询邮箱信息。
- `mailbox:disable`：禁用邮箱。
- `email:list`：查询邮件列表。
- `email:body`：查询邮件正文。
- `email:attachment`：下载邮件附件。

权限编码必须与接口要求完全相等，不做前缀或子串匹配。应用被停用、凭证被撤销或凭证到期后立即返回 401。

升级时，系统会将旧 `api_tokens` 和当时配置的 `auth.tokens` 一次性导入为哈希凭证，逐项验证后删除明文表。旧 `mailbox:create` 同时映射创建和禁用权限，旧 `email:read` 映射邮件列表、正文和附件权限。旧表删除后，`auth.tokens` 只能对应已导入的哈希凭证，不能签发新 Token；完成升级后应从配置文件移除。

### 1.3 JSON 响应信封

除附件下载接口外，接口返回统一 JSON 信封：

```json
{
  "code": 0,
  "message": "success",
  "data": {},
  "request_id": "..."
}
```

常见错误：

| HTTP 状态 | code | 含义 |
|-----------|------|------|
| 400 | 1001 / 1002 | 参数错误 |
| 401 | 1003 | 缺少或格式错误的 Authorization |
| 401 | 1004 | Token 无效或已禁用 |
| 403 | 1005 | Scope 不足 |
| 404 | 2001 / 2003 | 资源不存在 |
| 500 | 3001 / 5001 | 外部依赖或系统错误 |

### 1.4 URL 编码

邮箱地址出现在 path 中时必须进行 URL path escape，例如：

```text
order+1@example.com -> order+1@example.com 或 order%2B1@example.com
user@example.com    -> user@example.com 或 user%40example.com
```

建议客户端统一使用标准库的 path escape 方法生成 `{email}`，避免 `+`、`@`、空格等字符在不同 HTTP 客户端中被错误处理。邮箱地址出现在 query 中时使用 query escape。

---

## 2. 邮箱接口（出票中心）

### 2.1 创建或复用邮箱

```http
POST /api/v1/mailboxes
Authorization: Bearer <token with mailbox:create permission>
Content-Type: application/json
```

请求体：

```json
{
  "order_id": "ORDER-20260703-001",
  "domain_id": 1,
  "retention_days": 30
}
```

字段说明：

| 字段 | 必填 | 说明 |
|------|------|------|
| `order_id` | 是 | 业务订单号，接口按该字段幂等复用邮箱 |
| `domain_id` | 否 | 指定域名 ID；为空时由分配器选择 |
| `retention_days` | 否 | 兼容字段；实际邮件清理统一使用系统配置 `general.default_retention_days`，账号本身不会到期 |

响应：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "order_id": "ORDER-20260703-001",
    "email_address": "order-xxx@example.com",
    "local_part": "order-xxx",
    "domain": "example.com",
    "password": "generated-password",
    "server_id": 1,
    "created_at": "2026-07-03T10:00:00Z",
    "sync_status": "synced",
    "is_existing": false
  },
  "request_id": "..."
}
```

`is_existing=true` 表示该订单号此前已创建邮箱，本次返回已有邮箱。

### 2.2 按订单查询邮箱

```http
GET /api/v1/mailboxes/{order_id}
Authorization: Bearer <token with mailbox:read permission>
```

响应 `data` 为邮箱账号记录，包含 `email_address`、`local_part`、`password`、`domain_id`、`server_id`、`status`、`sync_status`、`retention_days`、`created_at` 等字段。兼容字段 `expires_at` 不再用于账号生命周期，新建邮箱默认不返回该字段。

### 2.3 禁用邮箱

```http
POST /api/v1/mailboxes/{order_id}/disable
Authorization: Bearer <token with mailbox:disable permission>
```

按订单号禁用邮箱并触发数据面删除/回收流程。旧 Token 的 `mailbox:create` scope 在兼容期内仍可调用该接口。

---

## 3. 邮件读取接口（大模型系统）

### 3.1 按订单查询邮件列表

```http
GET /api/v1/orders/{order_id}/emails?page=1&size=20
Authorization: Bearer <token with email:list permission>
```

该接口是正式对外开放的订单维度入口。`mgmt-system` 先通过订单邮箱映射定位邮箱账号及所属 mail-node，再复用邮箱维度的邮件列表查询；订单未绑定邮箱时返回 404。外部调用方不得直接访问 mail-node 的内部接口。

> **当前仍为雏形：** 订单入口目前只是基于现有单邮箱映射的兼容查询层，返回该邮箱的邮件列表，不应被视为严格的订单级邮件隔离边界。当前尚不支持一个订单关联多个邮箱后的聚合查询、邮件归属二次校验、业务字段筛选或游标分页；这些能力需要在后续版本继续完善。

响应：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "order_id": "ORDER-20260703-001",
    "email_address": "order-xxx@example.com",
    "page": 1,
    "size": 20,
    "total": 1,
    "messages": [
      {
        "message_id": "<message-id@example.com>",
        "mailbox": "order-xxx@example.com",
        "subject": "航变通知",
        "from": "Airline <notice@example.com>",
        "to": ["order-xxx@example.com"],
        "cc": [],
        "date": "2026-07-03T10:00:00Z",
        "received_at": "2026-07-03T10:00:01Z",
        "text_preview": "您的航班时间已变更...",
        "has_attachments": true,
        "attachments_count": 1,
        "attachments": [
          {
            "index": 0,
            "filename": "itinerary.pdf",
            "content_type": "application/pdf",
            "size": 12345,
            "disposition": "attachment"
          }
        ],
        "parse_status": "ok",
        "parse_error": ""
      }
    ]
  },
  "request_id": "..."
}
```

### 3.2 按邮箱查询邮件列表

```http
GET /api/v1/mailboxes/{email}/messages?page=1&size=20
Authorization: Bearer <token with email:list permission>
```

该接口是邮箱维度主入口。`{email}` 是邮箱地址的 path 参数，应进行 URL path escape。响应结构与按订单查询邮件列表一致，但不一定包含 `order_id`。

两个邮件列表入口都需要 `email:list` 权限，并共享相同的分页、排序和邮件解析语义。

### 3.3 获取邮件正文

```http
GET /api/v1/emails/{message_id}/body?mailbox={email}
Authorization: Bearer <token with email:body permission>
```

参数：

| 参数 | 位置 | 必填 | 说明 |
|------|------|------|------|
| `message_id` | path | 是 | 邮件 Message-ID 或系统 fallback ID |
| `mailbox` | query | 是 | 邮箱地址 |

响应 `data` 包含邮件列表字段，并额外包含：

- `text_body`：纯文本正文。
- `html_body`：HTML 正文，可能为空。
- `headers`：解析后的头部字段。

### 3.4 下载附件

```http
GET /api/v1/emails/{message_id}/attachments/{index}?mailbox={email}
Authorization: Bearer <token with email:attachment permission>
```

参数：

| 参数 | 位置 | 必填 | 说明 |
|------|------|------|------|
| `message_id` | path | 是 | 邮件 Message-ID 或系统 fallback ID |
| `index` | path | 是 | 邮件列表/正文中 `attachments[].index` |
| `mailbox` | query | 是 | 邮箱地址 |

成功响应不是 JSON 信封，而是附件二进制流：

```http
HTTP/1.1 200 OK
Content-Type: application/pdf
Content-Disposition: attachment; filename="itinerary.pdf"; filename*=UTF-8''itinerary.pdf

<binary bytes>
```

客户端应按 HTTP 状态码和响应头处理下载，不要按 JSON 解析成功响应体。上游 4xx/5xx 错误可能透传 JSON 错误体。

### 3.5 调用顺序与性能语义

管理后台和外部 API 共用 mail-node 的 Message-ID 路径索引，接口契约不因索引而变化。建议调用方保持“列表 -> 正文/附件”的顺序：

1. 邮件列表解析当前页时会预热该页 Message-ID 到 Maildir 路径的本地索引。
2. 后续正文、预览和附件请求命中索引后直接定位目标 EML，只完整解析目标邮件一次。
3. mail-node 重启后的第一次冷请求会扫描该邮箱的邮件头，但不会完整解析每一封正常邮件。
4. 索引只缓存路径和文件指纹，不缓存正文或附件字节；重复下载仍会解析目标 EML，并继续经过 mgmt-system 二进制代理。

因此，本地索引会同时改善管理端和所有外部调用方的正文/附件首字节延迟，但不会替代高并发重复下载所需的 MinIO、Range、CDN 或预签名 URL。容量边界见[部署容量与附件存储边界](../deployment-capacity.md)。

---

## 4. 已退役的过滤规则接口

旧 `/api/v1/filters` 及 `filter:read/create/update/delete` 权限已经退役，不再属于外部 API 契约。迁移期间，legacy 规则只能由 Session 鉴权的管理端维护；mail-node 继续通过内部 Shared-Secret 接口拉取，不影响现有邮件处理。

后续版本化的人工规则与广告策略 API 将使用独立 revision、validate、publish 和权限语义。在该新契约正式发布前，外部系统不得依赖管理端或内部 legacy 接口。

---

## 5. 权限与调用方建议

| 调用方 | 建议权限 | 用途 |
|--------|------------|------|
| 出票中心 | `mailbox:create,mailbox:read,mailbox:disable` | 创建、查询、禁用订单邮箱 |
| 大模型系统 | `email:list,email:body,email:attachment` | 拉取邮件列表、正文和附件 |

旧版本兼容配置示例（仅用于升级过渡）：

```yaml
auth:
  tokens:
    - name: "出票中心"
      token: "sk-ticket-xxx"
      scopes: ["mailbox:create", "mailbox:read"]
    - name: "大模型系统"
      token: "sk-llm-xxx"
      scopes: ["email:read"]
```

---

## 6. curl 示例

```bash
curl -X POST 'https://mail.example.com/api/v1/mailboxes' \
  -H 'Authorization: Bearer sk-ticket-xxx' \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"ORDER-20260703-001","retention_days":30}'
```

```bash
curl 'https://mail.example.com/api/v1/orders/ORDER-20260703-001/emails?page=1&size=20' \
  -H 'Authorization: Bearer sk-llm-xxx'
```

```bash
curl 'https://mail.example.com/api/v1/emails/%3Cmessage-id%40example.com%3E/body?mailbox=order-xxx%40example.com' \
  -H 'Authorization: Bearer sk-llm-xxx'
```

```bash
curl -L -o itinerary.pdf \
  'https://mail.example.com/api/v1/emails/%3Cmessage-id%40example.com%3E/attachments/0?mailbox=order-xxx%40example.com' \
  -H 'Authorization: Bearer sk-llm-xxx'
```
