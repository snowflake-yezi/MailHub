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
- `email:raw`：下载字节级原始 EML。
- `manual-filter:read`：读取生效或指定版本的人工过滤规则。
- `manual-filter:draft`：创建、修改和校验人工过滤规则草稿。
- `manual-filter:publish`：发布人工过滤规则版本。
- `ad-filter:read`：读取生效或指定版本的广告过滤策略。
- `ad-filter:draft`：创建、修改和校验广告过滤策略草稿。
- `ad-filter:publish`：发布广告过滤策略版本。

权限编码必须与接口要求完全相等，不做前缀或子串匹配。应用被停用、凭证被撤销或凭证到期后立即返回 401。

升级时，系统会将旧 `api_tokens` 和当时配置的 `auth.tokens` 一次性导入为哈希凭证，逐项验证后删除明文表。旧 `mailbox:create` 同时映射创建和禁用权限，旧 `email:read` 只映射邮件列表、正文和附件权限，不包含原始 EML。旧表删除后，`auth.tokens` 只能对应已导入的哈希凭证，不能签发新 Token；完成升级后应从配置文件移除。

### 1.3 JSON 响应信封

除附件和原始 EML 下载接口外，接口返回统一 JSON 信封：

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

### 3.5 下载原始 EML

```http
GET /api/v1/emails/{message_id}/raw?mailbox={email}
Authorization: Bearer <token with email:raw permission>
```

参数：

| 参数 | 位置 | 必填 | 说明 |
|------|------|------|------|
| `message_id` | path | 是 | 邮件 Message-ID 或系统 fallback ID |
| `mailbox` | query | 是 | 邮箱地址 |

成功响应不是 JSON 信封，而是 Maildir 中原始 EML 的字节流：

```http
HTTP/1.1 200 OK
Content-Type: message/rfc822
Content-Disposition: attachment; filename="message.eml"
Content-Length: 12345
Cache-Control: private, no-store
X-Content-Type-Options: nosniff

<raw EML bytes>
```

该接口不会解析后重建邮件，也不会使用 Base64 或 JSON 包装。调用方应先判断状态码，按 `Content-Length` 校验实际接收字节数，并在落库或写入对象存储时自行计算 SHA-256。上游 4xx/5xx 错误仍可能返回 JSON 错误信封。

`email:raw` 是独立的高敏感权限，不包含在旧 `email:read` 映射中，也不会自动授予现有应用。

### 3.6 调用顺序与性能语义

管理后台和外部 API 共用 mail-node 的 Message-ID 路径索引，接口契约不因索引而变化。建议调用方保持“列表 -> 正文/附件/原始 EML”的顺序：

1. 邮件列表解析当前页时会预热该页 Message-ID 到 Maildir 路径的本地索引。
2. 后续正文、预览、附件和原始 EML 请求命中索引后直接定位目标文件；正文和附件会完整解析目标邮件，原始 EML 接口直接读取文件。
3. mail-node 重启后的第一次冷请求会扫描该邮箱的邮件头，但不会完整解析每一封正常邮件。
4. 索引只缓存路径和文件指纹，不缓存正文、附件或 EML 字节；重复下载继续经过 mgmt-system 二进制代理。

因此，本地索引会同时改善管理端和所有外部调用方的正文、附件与原始 EML 首字节延迟，但不会替代高并发重复下载所需的 MinIO、Range、CDN 或预签名 URL。容量边界见[部署容量与附件存储边界](../deployment-capacity.md)。

---

## 4. 版本化过滤配置接口

旧 `/api/v1/filters` 及 `filter:read/create/update/delete` 权限已经退役，不再属于外部 API 契约。迁移期间，legacy 规则只能由 Session 鉴权的管理端维护；mail-node 继续通过内部 Shared-Secret 接口拉取，不影响现有邮件处理。

新接口将人工规则和广告策略分别版本化、分别授权。所有写入必须先创建 `draft`，完成修改和显式校验后再调用 `publish`；已发布版本不可修改，回滚时应以历史版本为 `base_revision` 创建新的单调递增草稿。

### 4.1 人工规则

| Method | Path | Permission | 用途 |
|--------|------|------------|------|
| GET | `/manual-filter-revisions/active` | `manual-filter:read` | 读取当前生效 bundle |
| POST | `/manual-filter-revisions` | `manual-filter:draft` | 创建空草稿或从 `base_revision` 克隆 |
| GET | `/manual-filter-revisions/:revision` | `manual-filter:read` | 读取指定版本及完整规则 |
| POST | `/manual-filter-revisions/:revision/rules` | `manual-filter:draft` | 新增规则 |
| PUT/DELETE | `/manual-filter-revisions/:revision/rules/:logical_id` | `manual-filter:draft` | 修改或删除规则 |
| POST | `/manual-filter-revisions/:revision/validate` | `manual-filter:draft` | 校验完整草稿并计算 checksum |
| POST | `/manual-filter-revisions/:revision/publish` | `manual-filter:publish` | 原子发布版本 |

创建回滚草稿：

```json
{
  "base_revision": 7
}
```

新增或更新规则的请求体：

```json
{
  "logical_id": "cf4dd635-b0ae-4cb8-936e-b488135699c9",
  "name": "放行航司通知域名",
  "scope_type": "global",
  "action": "allow",
  "mode": "shadow",
  "priority": 10,
  "source": "external",
  "conditions": [
    {
      "field": "header_from.domain",
      "operator": "eq",
      "value": "airline.example"
    }
  ]
}
```

### 4.2 广告策略

| Method | Path | Permission | 用途 |
|--------|------|------------|------|
| GET | `/ad-filter-revisions/active` | `ad-filter:read` | 读取当前生效 bundle |
| POST | `/ad-filter-revisions` | `ad-filter:draft` | 创建空草稿、克隆历史版本或导入审核 seed |
| GET | `/ad-filter-revisions/:revision` | `ad-filter:read` | 读取指定版本及完整策略图 |
| POST | `/ad-filter-revisions/:revision/detectors` | `ad-filter:draft` | 新增 detector |
| PUT/DELETE | `/ad-filter-revisions/:revision/detectors/:logical_id` | `ad-filter:draft` | 修改或删除 detector |
| POST | `/ad-filter-revisions/:revision/composites` | `ad-filter:draft` | 新增 composite |
| PUT/DELETE | `/ad-filter-revisions/:revision/composites/:logical_id` | `ad-filter:draft` | 修改或删除 composite |
| PUT/DELETE | `/ad-filter-revisions/:revision/weights/:symbol` | `ad-filter:draft` | 设置或删除 symbol weight |
| POST | `/ad-filter-revisions/:revision/validate` | `ad-filter:draft` | 校验完整草稿并计算 checksum |
| POST | `/ad-filter-revisions/:revision/publish` | `ad-filter:publish` | 原子发布版本 |

`POST /ad-filter-revisions` 接受且仅接受以下三种来源之一：空请求体、`{"base_revision": 7}` 或 `{"seed": "ad-seed-v1"}`。`base_revision` 与 `seed` 不能同时提供。

detector、composite 和 weight 的字段契约见[广告邮件过滤重构设计](../design/spam-filter-redesign.md#104-请求示例)。分值最多保留三位小数；symbol、condition、DAG、阈值关系及 bundle 大小在 validate/publish 时整批校验，不能部分发布。

### 4.3 幂等、审计与安全边界

- publish 请求应携带最长 64 字符的 `Idempotency-Key`；相同版本已处于 active 时，重复请求返回同一已发布结果，不创建新版本。
- 外部写操作同时记录应用身份、revision、操作类型和请求 ID；Bearer 调用记录还会写入独立 API 访问日志。
- 新权限只注册为可授权能力，不会自动授予任何现有或新建应用。`manual-filter:publish` 与 `ad-filter:publish` 应仅授予发布系统。
- `/active` 在对应策略从未发布时返回 404。读取 active 不表示节点已经收敛；节点 desired/applied 状态仅在管理端展示。
- 外部 API 不注册 decisions、quarantines、隔离正文、隔离附件、放行或反馈端点。普通邮件接口只扫描源 Maildir，因此隔离邮件也无法经邮件查询接口读取。
- 新接口不会切换 `filter.engine_mode` 或 `filter.auto_quarantine_enabled`。策略发布与生产 enforce/canary 是两个独立操作。

---

## 5. 权限与调用方建议

| 调用方 | 建议权限 | 用途 |
|--------|------------|------|
| 出票中心 | `mailbox:create,mailbox:read,mailbox:disable` | 创建、查询、禁用订单邮箱 |
| 大模型系统 | `email:list,email:body,email:attachment` | 拉取邮件列表、正文和附件 |
| 邮件归档系统 | `email:list,email:raw` | 定位邮件并流式归档字节级原始 EML |
| 策略同步系统 | `manual-filter:read,manual-filter:draft,ad-filter:read,ad-filter:draft` | 维护和校验草稿，不具备发布能力 |
| 策略发布系统 | 按需增加 `manual-filter:publish`、`ad-filter:publish` | 独立执行经审批的版本发布 |

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
