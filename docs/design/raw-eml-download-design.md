# 原始 EML 透传接口设计

## 1. 背景与目标

第三方归档系统需要获取 MailHub 保存的原始 EML，并将响应体直接流式写入对象存储。接口必须返回 Maildir 中已有文件的原始字节，不得把邮件解析后重新拼装，不得使用 JSON 或 Base64 包装成功响应。

本阶段只提供读取能力，不在 MailHub 内实现 OSS 上传、摘要持久化或历史 EML 解析。

## 2. 接口契约

外部接口：

```http
GET /api/v1/emails/{message_id}/raw?mailbox={email}
Authorization: Bearer <token with email:raw permission>
```

内部接口：

```http
GET /internal/messages/{message_id}/raw?mailbox={email}
X-Internal-Token: <shared-secret>
```

成功响应例外于统一 JSON 信封：

```http
HTTP/1.1 200 OK
Content-Type: message/rfc822
Content-Disposition: attachment; filename="message.eml"
Content-Length: <原始文件字节数>
Cache-Control: private, no-store
X-Content-Type-Options: nosniff

<Maildir EML 原始字节>
```

`message.eml` 使用固定文件名，避免在下载头中暴露邮箱地址或不可信的 Message-ID。错误响应仍使用 JSON 信封；调用方必须先判断 HTTP 状态码，再处理响应体。

## 3. 数据流与职责

```text
第三方系统
  -> mgmt-system：Bearer Token 鉴权，要求 email:raw
  -> mail-node：Shared-Secret 鉴权，按 mailbox + message_id 定位 Maildir 文件
  -> os.Open + 流式复制
  -> mgmt-system 流式代理
  -> 第三方接收原始字节
```

- `mail-node` 可以读取 RFC 5322 头部来定位 Message-ID，但响应体始终来自原始文件，不能来自 MIME 解析结果。
- `mgmt-system` 只透传允许的响应头、状态码和响应体，不读取整封邮件到内存。
- `Content-Length` 必须透传。第三方应校验实际接收字节数，并自行计算 SHA-256 后归档。
- 请求链路受两端 HTTP Server 的写超时约束。内部代理只限制建连和响应头等待，不使用覆盖整个下载过程的短超时。

## 4. 权限边界

新增独立权限 `email:raw`。它不会隐式包含在 `email:list`、`email:body`、`email:attachment` 或旧 `email:read` 映射中，也不会自动授予现有应用。管理员必须显式向归档调用方授权。

mail-node 端点继续只允许带内部 Shared-Secret 的控制面调用，不直接暴露公网。

## 5. 错误语义

| 场景 | HTTP 状态 | 响应 |
|------|-----------|------|
| mailbox 参数缺失或非法 | 400 | JSON 错误信封 |
| 邮箱、服务器或邮件不存在 | 404 | JSON 错误信封 |
| 缺少 `email:raw` | 403 | JSON 错误信封 |
| Maildir 文件无法打开或读取 | 500 或连接中断 | 写响应前返回 JSON；写出后由字节数不匹配识别 |

HTTP 状态一旦写出就无法在中途读取失败时改写。调用方不能仅凭 `200` 判断归档成功，必须验证 `Content-Length` 和自身持久化结果。

## 6. 非目标

- 不支持解析后重建 EML。
- 不支持 Base64 或 JSON 内嵌 EML。
- 不提供公网永久 URL、预签名 URL、Range 或断点续传。
- 不读取隔离目录中的 EML；隔离邮件继续使用专用管理接口。
- 不改变正文和附件接口的现有行为。

## 7. 验收标准

1. 包含 CRLF、NUL 和非 UTF-8 字节的 EML 经 mail-node 与 mgmt-system 后逐字节一致。
2. 成功响应为 `message/rfc822`，并包含正确的 `Content-Length`、下载文件名和 `nosniff`。
3. 成功路径不使用 JSON/Base64，不调用 MIME parser 生成响应体。
4. 缺少 `email:raw` 的 Token 返回 403；旧 `email:read` 不获得该能力。
5. 不存在的邮件继续返回可识别的 JSON 404。
