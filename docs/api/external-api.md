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

Token 在 `auth.tokens` 配置或 `api_tokens` 表中维护。`scopes` 使用逗号分隔的完整项：

- `mailbox:create`：创建/禁用邮箱。
- `mailbox:read`：查询邮箱信息。
- `email:read`：查询邮件列表、正文和附件。
- `*`：通配全部 scope，仅用于受控调试或管理员级系统。

Scope 校验规则：按逗号分隔后 trim，每一项必须与所需 scope 完全相等；不做子串匹配。

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
Authorization: Bearer <token with mailbox:create>
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
| `retention_days` | 否 | 保留天数；为空使用系统默认值 |

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
    "expires_at": "2026-08-02T10:00:00Z",
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
Authorization: Bearer <token with mailbox:read>
```

响应 `data` 为邮箱账号记录，包含 `email_address`、`local_part`、`password`、`domain_id`、`server_id`、`status`、`sync_status`、`retention_days`、`created_at`、`expires_at` 等字段。

### 2.3 禁用邮箱

```http
POST /api/v1/mailboxes/{order_id}/disable
Authorization: Bearer <token with mailbox:create>
```

按订单号禁用邮箱并触发数据面删除/回收流程。当前为兼容出票中心 Token，仍复用 `mailbox:create` scope。

---

## 3. 邮件读取接口（大模型系统）

### 3.1 按订单查询邮件列表

```http
GET /api/v1/orders/{order_id}/emails?page=1&size=20
Authorization: Bearer <token with email:read>
```

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
Authorization: Bearer <token with email:read>
```

该接口是邮箱维度主入口。`{email}` 是邮箱地址的 path 参数，应进行 URL path escape。响应结构与按订单查询邮件列表一致，但不一定包含 `order_id`。

### 3.3 获取邮件正文

```http
GET /api/v1/emails/{message_id}/body?mailbox={email}
Authorization: Bearer <token with email:read>
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
Authorization: Bearer <token with email:read>
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

---

## 4. Scope 与调用方建议

| 调用方 | 建议 Scope | 用途 |
|--------|------------|------|
| 出票中心 | `mailbox:create,mailbox:read` | 创建、查询、禁用订单邮箱 |
| 大模型系统 | `email:read` | 拉取邮件列表、正文和附件 |
| 联调管理员 | `*` | 临时全权限联调，生产慎用 |

配置示例：

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

## 5. curl 示例

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
