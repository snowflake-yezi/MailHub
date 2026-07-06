# inline 图片文件后缀解析修复设计

> 状态：待实现 | 日期：2026-07-06 | 关联：`docs/design/t8-mime-preprocessing-design.md`、`docs/design/attachment-download-design.md`

---

## 1. 背景与问题

用户反馈：正文内嵌图片已经能接收，但「文件后缀解析有问题」，并且接收后在邮箱后台也无法稳定展示。现场检查 union 邮箱中的 QQ 图片邮件可见：

```text
Subject: 图片测试
Content-Type: multipart/related
Content-Type: application/octet-stream
Content-ID: <240607BE@75E5D830.2D644B6A00000000>
```

这类邮件的正文图片不是普通附件，而是 MIME inline part。部分客户端（QQ/Gmail/移动端）会出现以下情况：

- inline 图片 `Content-Type` 不准确，例如 `application/octet-stream`。
- inline 图片没有 `filename` / `name`。
- 只有 `Content-ID`，HTML 正文通过 `cid:` 引用。
- 图片真实字节是 PNG/JPEG/GIF/WebP/BMP，但 MIME header 没给出可靠后缀。

当前代码在 `mail-node/internal/handler/message_parser.go` 中使用：

```go
filename := strings.TrimSpace(part.FileName)
if filename == "" {
    filename = fmt.Sprintf("attachment-%d", index)
}
ContentType: part.ContentType
```

下载端点 `mail-node/internal/handler/node.go` 又单独使用另一套逻辑：

```go
contentType := strings.TrimSpace(part.ContentType)
if contentType == "" {
    contentType = "application/octet-stream"
}
filename := attachmentFilename(part, index)
```

因此当 QQ inline 图片缺文件名且 Content-Type 是 `application/octet-stream` 时，系统只能返回：

```json
{
  "filename": "attachment-0",
  "content_type": "application/octet-stream",
  "content_id": "...",
  "inline": true
}
```

这会导致：

1. 后台附件列表没有正确图片后缀。
2. 下载时默认文件名无 `.png/.jpg`。
3. HTML 预览把 `cid:` 映射到附件下载 URL 后，浏览器拿到的响应仍可能是 `application/octet-stream`，无法稳定按图片渲染。
4. 元数据接口和下载接口各自兜底，可能出现后台显示与下载响应不一致。

---

## 2. 目标

1. 对缺失文件名/后缀的 inline 图片，根据字节魔数推断真实图片类型。
2. 当 MIME `Content-Type` 是空或 `application/octet-stream` 时，用推断结果修正为 `image/png` / `image/jpeg` / `image/gif` / `image/webp` / `image/bmp`。
3. 当文件名没有扩展名时，补齐合理后缀，例如 `inline-0.png`。
4. 保持普通附件现有行为不变，避免误改 PDF/ZIP 等文件。
5. 保证元数据接口和下载接口使用一致的文件名/Content-Type 推断逻辑。
6. 提升后台 HTML 预览中 inline 图片展示兼容性。

---

## 3. 非目标

- 不做完整文件类型识别库引入。
- 不解析图片尺寸。
- 不做 OSS 上传或图片缩略图。
- 不对已有扩展名的文件强行改名。
- 不覆盖明确的非图片 Content-Type，例如 `application/pdf` / `application/zip`。
- 不自动加载外部远程图片。
- 第一版不推断 SVG。SVG 是文本格式，可能携带脚本或外链，需另行安全评审。

---

## 4. 设计方案

### 4.1 新增 MIME part 文件类型推断 helper

在 `mail-node/internal/handler/message_parser.go` 中新增小型 helper，供元数据解析和下载端点复用。

建议类型：

```go
type inferredPartInfo struct {
    Filename    string
    ContentType string
}
```

建议函数：

```go
func inferPartInfo(part *enmime.Part, index int, inline bool) inferredPartInfo
```

职责：

1. 读取原始字段：
   - `part.FileName`
   - `part.ContentType`
   - `part.Content`
2. 根据 `part.Content` 的魔数尝试推断图片类型。
3. 仅当原始 `Content-Type` 为空或为 `application/octet-stream` 时，使用推断出的图片 Content-Type 覆盖。
4. 若 `Content-Type` 最终仍为空，则兜底为 `application/octet-stream`。
5. 若文件名为空：
   - inline part 用 `inline-<index><ext>`。
   - 普通附件用 `attachment-<index><ext>`。
6. 若文件名存在但无扩展名，且能从魔数或明确 `image/*` Content-Type 推断出图片扩展名，则追加扩展名。
7. 若文件名已有扩展名，保持原样，不把 `.jpeg` 改成 `.jpg`。

### 4.2 支持的图片魔数

| 类型 | 魔数 / 判断 | Content-Type | 扩展名 |
|------|-------------|--------------|--------|
| PNG | `89 50 4E 47 0D 0A 1A 0A` | `image/png` | `.png` |
| JPEG | `FF D8 FF` | `image/jpeg` | `.jpg` |
| GIF | `GIF87a` / `GIF89a` | `image/gif` | `.gif` |
| WebP | `RIFF....WEBP` | `image/webp` | `.webp` |
| BMP | `BM` | `image/bmp` | `.bmp` |

说明：

- 第一版只支持位图格式，不支持 SVG。
- 魔数判断必须尽量精确，避免把非图片二进制误判为图片。

### 4.3 元数据解析使用推断结果

修改：`mail-node/internal/handler/message_parser.go`

当前：

```go
func attachmentFromPart(index int, part *enmime.Part, inline bool) parsedAttachment {
    filename := strings.TrimSpace(part.FileName)
    if filename == "" {
        filename = fmt.Sprintf("attachment-%d", index)
    }
    ...
    ContentType: part.ContentType,
}
```

调整为：

```go
info := inferPartInfo(part, index, inline)
...
Filename: info.Filename,
ContentType: info.ContentType,
```

这样 `/api/v1/admin/emails/:message_id/body` 返回的 `attachments[]` 就能显示正确后缀和 content_type。

### 4.4 下载端点使用同一推断结果

修改：`mail-node/internal/handler/node.go`

当前下载端点独立取：

```go
contentType := strings.TrimSpace(part.ContentType)
filename := attachmentFilename(part, index)
```

调整为复用同一个推断 helper：

```go
inline := index >= len(envelope.Attachments) || strings.EqualFold(strings.TrimSpace(part.Disposition), "inline")
info := inferPartInfo(part, index, inline)
contentType := info.ContentType
filename := info.Filename
```

确保：

- JSON 元数据看到的是 `.png`，下载响应也是 `.png`。
- JSON 元数据看到的是 `image/png`，下载响应也是 `Content-Type: image/png`。
- HTML 预览中 `<img src="...attachments/:index">` 获得浏览器更容易渲染的图片 Content-Type。

### 4.5 文件名策略

| 场景 | 文件名策略 |
|------|------------|
| 原文件名 `logo.png` | 保持 `logo.png` |
| 原文件名 `logo`，推断 PNG | `logo.png` |
| 原文件名为空，inline PNG | `inline-<index>.png` |
| 原文件名为空，普通附件 PNG | `attachment-<index>.png` |
| 原文件名为空，无法推断 | inline: `inline-<index>`；普通附件: `attachment-<index>` |
| 原文件名 `photo.jpeg`，推断 JPEG | 保持原名，不强制改成 `.jpg` |
| 原 Content-Type 明确 `image/jpeg` 但文件名无扩展 | 补 `.jpg` |
| 原 Content-Type 明确 `application/pdf` | 不按图片魔数外的规则改写 |

### 4.6 普通附件保护原则

- 明确的非图片 `Content-Type` 不覆盖。
- 已有扩展名的文件名不改写。
- PDF/ZIP/Office 等普通附件不因本次变更改变命名策略。
- 推断逻辑只修复“不可信 Content-Type + 缺失后缀”的图片展示问题。

---

## 5. 测试计划

### 5.1 单元测试：文件类型推断

新增/扩展 `mail-node/internal/handler/message_parser_test.go`：

1. inline PNG：
   - `Content-Type: application/octet-stream`
   - 无 filename
   - 真实 PNG magic bytes
   - 期望：`filename=inline-0.png`、`content_type=image/png`。

2. inline JPEG：
   - 无 filename
   - JPEG magic bytes
   - 期望：`.jpg`、`image/jpeg`。

3. 已有文件名但无扩展：
   - filename=`qq-inline`
   - PNG bytes
   - 期望：`qq-inline.png`。

4. 已有正确文件名：
   - filename=`logo.png`
   - 期望保持不变。

5. 普通 PDF 附件：
   - 保持 `application/pdf` 和原文件名，不被图片推断误改。

6. 未知 octet-stream inline part：
   - 无 filename
   - 无匹配 magic bytes
   - 期望：`inline-<index>`、`application/octet-stream`。

### 5.2 下载端点测试

扩展 `mail-node/internal/handler/node_test.go`：

- 构造 inline PNG part：
  - `Content-Type: application/octet-stream`
  - `Content-Disposition: inline`
  - `Content-ID: <...>`
  - 无 filename 或 filename 无扩展
  - body 使用真实 PNG magic bytes 的 base64
- 请求 `/internal/messages/:message_id/attachments/:index?mailbox=...`
- 断言：
  - `Content-Type` 包含 `image/png`
  - `Content-Disposition` 包含 `filename*=UTF-8''inline-<index>.png`
  - body 字节不变

---

## 6. 验证计划

本地：

```bash
go test -C mail-node ./internal/handler
go test -C mail-node ./...
go test -C mgmt-system ./...
npm --prefix mgmt-system/web run build
```

国际机：

1. 交叉编译并部署 `mail-node`。
2. 可选部署 `mgmt-system`（如果只改 mail-node 推断逻辑，mgmt 二进制不一定需要变；但 React 静态资源若已更新则保持当前部署即可）。
3. 向 `union@asadad.bond` 发送正文内嵌图片邮件。
4. 检查后台 `/admin/emails` 中该邮件附件元数据：
   - inline 图片 filename 有 `.png/.jpg` 后缀。
   - content_type 是 `image/*`，不是 `application/octet-stream`。
5. 点击 HTML 预览，图片能显示。
6. 下载 inline 图片，保存文件名带正确后缀。

---

## 7. 风险与回滚

### 风险

- 魔数推断过度可能误判非图片二进制。
- 不同客户端生成的 MIME header 差异较大，需要保持 fallback 稳定。
- inline 判断如果和附件 index 顺序不一致，可能导致元数据和下载命名不同。

### 缓解

- 第一版仅支持明确魔数的位图格式：PNG/JPEG/GIF/WebP/BMP。
- 只在 Content-Type 缺失或 `application/octet-stream` 时覆盖。
- 文件名已有扩展名时不强行改写。
- 普通附件行为尽量保持原样。
- 元数据和下载端点共用 `inferPartInfo`，并继续复用 `collectAttachmentParts` 保证 index 对齐。

### 回滚

- 回滚 `mail-node` 二进制即可恢复原行为。
- 该改动不涉及数据库 schema 和数据迁移。
