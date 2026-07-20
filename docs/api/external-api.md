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
- `filter:read`：查询过滤规则。
- `filter:create`：创建过滤规则。
- `filter:update`：更新过滤规则。
- `filter:delete`：删除过滤规则。

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

## 4. 过滤规则接口

过滤规则按 `priority ASC, id ASC` 执行，数值越小越先匹配。规则类型支持 `whitelist_sender`、`blacklist_sender`、`keyword`、`regex`，动作支持 `pass`、`block`、`flag`。

### 4.1 查询过滤规则

```http
GET /api/v1/filters
Authorization: Bearer <token with filter:read permission>
```

响应 `data` 是包含启用和停用规则的数组：

```json
{
  "code": 0,
  "message": "success",
  "data": [
    {
      "id": 12,
      "name": "拦截广告域名",
      "rule_type": "blacklist_sender",
      "pattern": "@ads.example",
      "action": "block",
      "priority": 20,
      "enabled": true,
      "created_at": "2026-07-20T10:00:00+08:00",
      "updated_at": "2026-07-20T10:00:00+08:00"
    }
  ],
  "request_id": "..."
}
```

### 4.2 创建过滤规则

```http
POST /api/v1/filters
Authorization: Bearer <token with filter:create permission>
Content-Type: application/json
```

请求体：

```json
{
  "name": "标记促销标题",
  "rule_type": "keyword",
  "pattern": "限时优惠",
  "action": "flag",
  "priority": 50,
  "enabled": true
}
```

`name` 和 `pattern` 必填；`rule_type` 默认为 `keyword`，`action` 默认为 `pass`。成功返回 HTTP 201，`data` 为创建后的完整规则。

### 4.3 更新过滤规则

```http
PUT /api/v1/filters/{id}
Authorization: Bearer <token with filter:update permission>
Content-Type: application/json
```

请求体字段与创建接口相同。该接口采用完整更新语义，调用方应始终传入 `priority` 和 `enabled`；缺省时分别按 `0` 和 `false` 处理。成功响应的 `data` 为更新后的完整规则。

### 4.4 删除过滤规则

```http
DELETE /api/v1/filters/{id}
Authorization: Bearer <token with filter:delete permission>
```

创建、更新和删除在数据库提交后会异步通知健康的 mail-node 立即重载；单次通知失败不会回滚接口结果，节点仍会通过周期同步收敛到最新规则。

---

## 5. 权限与调用方建议

| 调用方 | 建议权限 | 用途 |
|--------|------------|------|
| 出票中心 | `mailbox:create,mailbox:read,mailbox:disable` | 创建、查询、禁用订单邮箱 |
| 大模型系统 | `email:list,email:body,email:attachment` | 拉取邮件列表、正文和附件 |
| 规则管理服务 | `filter:read,filter:create,filter:update,filter:delete` | 查询并维护过滤规则 |

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

```bash
curl -X POST 'https://mail.example.com/api/v1/filters' \
  -H 'Authorization: Bearer sk-filter-xxx' \
  -H 'Content-Type: application/json' \
  -d '{"name":"标记促销标题","rule_type":"keyword","pattern":"限时优惠","action":"flag","priority":50,"enabled":true}'
```
