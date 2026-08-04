# MIME 正文投影、媒体识别与安全预览设计 v2

> 版本：v2.2 | 日期：2026-08-03 | 状态：实施契约已冻结，待执行
>
> 权威性：本文 v2.2 取代历史 MIME v1/v2.0/v2.1 方案；旧版本不再作为实施或验收依据。文中的 `*.v1` 若出现，仅表示新 wire contract/capability 自身的首个版本。
>
> 适用代码：`mail-node` MIME 树解释、正文选择、附件/正文资源、下载/预览、隔离区、DataStream、转发和管理端安全展示。
>
> 实施契约：[`mime-media-detection-implementation-plan.md`](mime-media-detection-implementation-plan.md)

## 1. 结论与边界

本方案是 MIME v2 的唯一设计契约：`BodyProjector` 负责正文、alternative/related scope 和 part 角色，`MediaResolver` 负责 decoded bytes 类型证据与策略，Safe Renderer 负责最终展示。任何消费者不得从展平附件列表反推 MIME 树，也不得在 handler、转发或前端重新定义 part 语义。

共享阶段 `S0/B1/B2` 未完成前，不得把当前 `Envelope.Text/HTML` 和附件顺序固化到异步读模型。实施顺序统一以配套实施计划的 DAG 为准。

本方案解决三类问题：

1. 对 mixed/alternative/related/signed/report/digest 和嵌套消息产生稳定正文、角色、path、warning 与兼容 attachment index。
2. inline part 只有 `Content-ID`，声明为 `application/octet-stream` 或没有文件名时，仍能得到稳定类型、文件名、后缀和正文资源权限。
3. 管理端可以在受限 iframe 中显示明确允许的图片、视频和音频，不加载远程资源，不执行邮件活动内容。

它不承诺“识别即可信”或“所有客户端都能播放”。文件类型识别是最佳努力证据，不能替代杀毒、完整性校验、解码器兼容性检查或媒体转码。

当前已存在的行为必须保留：

- `enmime` 是邮件 MIME 树和传输编码的唯一语义解析器；v2 使用统一配置的 `enmime.NewParser(...)`，不再要求字面调用默认 `enmime.ReadEnvelope`。
- `AttachmentParts` 继续按普通附件在前、inline part 在后的顺序生成 index。
- 原始 EML 不原地修改。
- PNG/JPEG/GIF/WebP/BMP 的 octet-stream inline 兼容不能回归。
- 外部 API 的 `filename/content_type/size/disposition/content_id/inline/index` 字段不增加必填字段。

## 2. 当前代码约束

| 链路 | 当前事实 | v2 要求 |
|---|---|---|
| MIME 解析 | `mailparse.Parse*` 调用默认 `enmime.ReadEnvelope`，附件内容已解码；默认 `SkipMalformedParts=false`；只有 `ParseFile` 当前有解码前 25 MiB 检查 | 所有入口统一 raw EML 硬限制和显式 parser options；媒体检测只消费已解码 `part.Content`，不声称限制 enmime 解码内存 |
| 查询 | `mailparse.Attachments` 调用 `InferPartInfo` | 改为唯一 `ResolvePartInfo` |
| HTTP 下载/预览 | `node.go` 每次重新解析整封 EML；预览上限 10 MiB | 共享决策；Range 仍先完整解析，不能宣称解决内存问题 |
| 隔离区/DataStream | `control_data.go`、`quarantine.go` 各自组装响应 | 共享响应策略和字节范围 helper |
| 转发 | `smtp.go` 已有递归 raw walker 并只修五种图片，但使用全局 lowercase CID、宽松 delimiter，且不能报告事务失败 | raw locator 只消费 projector evidence，按 scope/path 定位并保证失败整封原样 |
| DataStream | receiver 已支持 `ContentLength=-1` 跳过长度比较并保留 checksum；dispatcher 会从 HTTP `Content-Length` 反推协议长度 | 增加独立协议长度字段并阻止 HEAD 反推；补空 body HEAD 测试，不重复实现 receiver 语义 |
| 前端正文 | `EmailsPage.jsx` 只重写 `img[src=cid:]`，允许任意 `data:image/*` | 显式媒体白名单；typed `ReferenceMap` 同时支持 CID/Content-Location 并保存类型、inline 和 URL |

## 3. 架构

```text
Raw EML --hard byte limit--> enmime decoded tree
                                  |
                                  v
                    post-decode semantic limits
                                  |
                                  v
                 BodyProjector (path/role/scope/body)
                                  |
                                  v
             MediaResolver (declared/detected/risk/policy)
                    |             |             |
                 metadata      resources      forward evidence
                    |             |             |
                    +------ Safe Renderer ------+
```

依赖方向必须单向。`BodyProjector` 不访问 HTTP、数据库或 BlobStore；`ResolvePartInfo` 不读文件、不重新遍历原始 EML、不依赖 handler；Safe Renderer 不推断真实媒体类型。`enmime` 是唯一 MIME 语义解析器，转发 raw locator 只能消费已冻结 evidence 定位 byte span。

## 4. 统一内部模型

```go
type PartPath []int

type PartRole string
const (
    RoleBodyPlain       PartRole = "body_plain"
    RoleBodyHTML        PartRole = "body_html"
    RoleRelatedResource PartRole = "related_resource"
    RoleAttachment      PartRole = "attachment"
    RoleEmbeddedMessage PartRole = "embedded_message"
    RoleReport          PartRole = "report"
    RoleSignature       PartRole = "signature"
    RoleEncrypted       PartRole = "encrypted"
    RoleUnknown         PartRole = "unknown"
)

type BodyView struct {
    GroupID      string
    PlainPath    PartPath
    HTMLPath     PartPath
    SelectedPath PartPath
    RelatedRoot  PartPath
}

type ProjectedPart struct {
    Path, ParentPath               PartPath
    Role                           PartRole
    DeclaredContentType            string
    Disposition, Filename          string
    ContentID, ContentLocation     string
    DecodedSize                    int64
    ExternalIndex                  *int
    ReferencedBy                   []PartPath
}

type ParseWarning struct {
    Code string
    Path PartPath
}

type ParseStatus string
const (
    ParseOK       ParseStatus = "ok"
    ParsePartial  ParseStatus = "partial"
    ParseFailed   ParseStatus = "failed"
    ParseTooLarge ParseStatus = "too_large"
)

type ParseResult struct {
    ParserVersion string
    PolicyVersion string
    Status        ParseStatus
    ErrorCode     string
    Message       *ParsedMessage
    PrimaryView   *BodyView
    BodyViews     []BodyView
    Parts         []ProjectedPart
    Warnings      []ParseWarning
    Features      filtercontract.MailFeatures
}

type PartInfo struct {
    Filename             string
    DeclaredContentType  string
    DetectedContentType  string
    EffectiveContentType string
    Extension            string
    TypeSource           PartTypeSource
    DetectionStrength    DetectionStrength
    MediaClass           MediaClass
    Conflict             ConflictKind
    ContentRisk          ContentRisk
    PreviewDeniedReason  string
    Inline               bool
}

type PartTypeSource string
const (
    PartTypeDeclared  PartTypeSource = "declared"
    PartTypeDetected  PartTypeSource = "detected"
    PartTypeFallback  PartTypeSource = "fallback"
)

type DetectionStrength string
const (
    DetectionNone   DetectionStrength = "none"
    DetectionWeak   DetectionStrength = "weak"
    DetectionStrong DetectionStrength = "strong"
)

type ConflictKind string
const (
    ConflictNone   ConflictKind = "none"
    ConflictStrong ConflictKind = "strong"
    ConflictDangerous ConflictKind = "dangerous"
)

type ContentRisk string
const (
    RiskNone       ContentRisk = "none"
    RiskActive     ContentRisk = "active_content"
    RiskExecutable ContentRisk = "executable"
)
```

`PartPath` 只在当前 parser revision 内作为结构身份，不直接暴露。`Status!=ok` 时 `ErrorCode` 必须来自有限枚举，`ok` 时必须为空；首版固定非空 `body-projector.v2/mime-policy.v2`。`ParsedAttachment.ContentType` 仍映射 `EffectiveContentType`，诊断字段只进入内部日志和测试。

### 4.1 查询身份与 MIME 派生身份

邮件查找身份必须独立于 MIME 投影。所有 list/detail/raw/attachment/body-resource lookup 共用一个最多读取 `1 MiB` RFC 5322 header 的 `MessageIdentity` scanner；它只提取和规范化 Message-ID，不解码 MIME body。header 缺失、损坏或超过上限时使用 fallback physical identity，不能因为正文 MIME 超限、解析失败或 panic 把已按路径命中的邮件改成 404。

physical identity 冻结为 `mailbox stable ID + Maildir unique name`，其中 unique name 不包含 `new/cur` 目录和 `:2,flags` 后缀，因此 `new -> cur` 或仅 flag 变化不改变身份。资源 token 另绑定 physical revision fingerprint：mailbox、stable unique name、文件 size、mtime 和 raw SHA-256 任一变化都使旧 token stale。外部 Message-ID 只用于兼容查找，不单独承担资源授权或 token 防重放。

## 5. MIME 树与兼容投影

### 5.1 通用与正文规则

- 所有解析入口使用 `enmime.NewParser(...)`，固定等价选项 `SkipMalformedParts(true)`、`MaxStoredPartErrors(8)`（每个保留 part）和 `DisableTextConversion(true)`；不得混用默认 parser。正文转换只由 `BodyProjector` 执行。
- 递归遍历保留源顺序和完整 `PartPath`。被跳过 child 的 enmime error 只能绑定到最近的已保留父 path，因为 child 已丢弃后无法恢复精确 path；不得伪造一个看似精确的 child path。可用 sibling 继续投影并返回 `partial/mime_partial`。
- 顶层 header/root 解析失败、顶层 missing boundary 和无法构造根树是 `failed/mime_parse_failed`；child malformed/missing boundary 在 parser 成功跳过时是 `partial/mime_partial`。parser 入口设置普通 Go panic recover 边界并映射为 `failed/parser_panic`，记录计数和内部 stack；不承诺恢复 runtime fatal error 或 stack overflow。
- `Content-Disposition: attachment` 优先于 `text/*`，附件不能成为正文。只有 decoded `text/plain`、`text/html` 和明确支持的报告文本能成为正文段。
- `ParsedMessage.TextBody/HTMLBody` 由 BodyProjector 生成，不再直接复制 `Envelope.Text/HTML`。HTML-only 使用结构化 HTML-to-text 生成兼容 text 并记录 `plain_generated_from_html`；attachment/signature/embedded/未选中 alternative 不参与生成。
- primary 只返回原始 decoded HTML，清洗和 CID 替换属于展示投影，不覆盖 canonical `html_body`。

### 5.2 容器规则

- `multipart/alternative` 从后向前选择可安全展示分支；同组可保留 plain/HTML path，但 `SelectedPath` 唯一。未选中分支不得提供正文资源。
- `multipart/related` 优先按 `start` 精确匹配 root，否则使用首 child；损坏时回退首个可展示 child 并 warning。引用只解析当前 related scope 的 sibling/descendant。
- CID 精确值优先；仅唯一候选允许 ASCII case-fold fallback，冲突记录 `ambiguous_content_id` 并拒绝。Content-Location 只解析当前 scope 的相对引用，绝对 HTTP(S) 始终阻断。
- `multipart/mixed` 首个可展示 group 为 primary，后续 plain 可作为有界 supporting segment；v2 不自动拼接 supporting HTML。
- `multipart/signed` 只投影首 child 正文，签名标记 RoleSignature；encrypted 不伪造正文；report 的 human-readable child 可为正文，机器 part 为 RoleReport；digest/message-rfc822 不把嵌套正文提升到外层。
- 顶层 `message/rfc822/message/global` 标记 RoleEmbeddedMessage；角色本身不自动改变旧附件 API。

### 5.3 attachments 与 body resources

adapter 必须先冻结 enmime 当前分类，再计算角色：

| enmime 分类 | ExternalIndex / 旧附件 API |
|---|---|
| `Envelope.Attachments` | 保留，按原顺序从 0 编号，角色不得删除或重排 |
| `Envelope.Inlines` | 保留，接在全部 Attachments 后，角色不得删除或重排 |
| `Envelope.OtherParts` | `ExternalIndex=nil`，不进入 attachments/count/index |
| 正文候选 | `ExternalIndex=nil` |

有效引用的 related resource 即使属于 `OtherParts`，也可进入可选 `body_resources[]` 并获得 message-bound opaque token；token 只定位正文资源，不替代鉴权、不赋予普通下载能力、不进入 parser golden。无 disposition embedded/report/signature 不得为“可下载”而插入旧 index。

## 6. 类型识别与决策

### 6.1 声明类型规范化

先用 `mime.ParseMediaType` 去除参数并转小写。以下声明视为通用类型：空字符串、`application/octet-stream`、`binary/octet-stream`、`application/x-download`。

兼容别名集中维护：

- `image/jpg` -> `image/jpeg`
- `audio/x-wav` -> `audio/wav`
- `video/x-matroska` -> `video/matroska`

### 6.2 检测窗口

当前使用的 `mimetype.Detect` 自身有进程级读取上限，单纯传入 64 KiB 不会扩大窗口。mail-node 进程 bootstrap 必须在 Gin validator 和任何媒体检测可能运行前执行一次：

```go
mimetype.SetLimit(64 * 1024)
```

该设置是进程全局状态，当前 v1.4.2 也由 Gin validator 间接使用。`mailparse.ConfigureDetector` 使用 `sync.Once` 封装，`cmd/node` 在构造 router/handler 前显式调用；单元测试同时验证 3 KiB 之后的签名和 validator 相关回归。该值不是热配置。若部署不接受全局副作用，则固定退回 3072 字节并同步修改本设计，不能保留“64 KiB”但实际只读 3072 的描述。

检测结果分为：

- **strong**：PNG/JPEG/GIF/WebP/BMP/AVIF/HEIC、MP4/WebM/Ogg、MP3/WAV/FLAC、PDF、ZIP、Office、可执行文件等有明确二进制签名的类型。
- **weak**：`text/plain`、部分 XML/JSON 和仅依赖可打印字符的结果。
- **none**：`application/octet-stream` 或空内容。

DetectionStrength 只描述检测器证据质量，不承担安全分类。只要声明类型或检测器的精确结果识别出 HTML、XHTML、SVG、JavaScript、脚本或可执行格式，就设置 `ContentRisk`，优先执行 deny/degrade；不能把任意 printable text 武断识别成 JavaScript。危险类型表必须按完整 MIME 类型维护，并用当前锁定的 mimetype 版本证明每个“detected risk”样本确实能得到对应结果。

检测失败永远只产生 fallback，不得使整封邮件解析失败。

### 6.3 决策矩阵

| 声明 | 检测证据 | EffectiveContentType | Conflict | 预览 |
|---|---|---|---|---|
| 空/通用 | strong 已知类型 | 检测类型 | none | 按白名单 |
| 空/通用 | weak 类型 | 检测类型 | none | 默认不允许活动媒体 |
| 空/通用 | none | `application/octet-stream` | none | 否 |
| 明确类型 | 同类型或兼容别名 | 规范化声明 | none | 按白名单 |
| 明确类型 | weak 不同类型 | 规范化声明 | none | 仍按声明类型策略 |
| 明确类型 | strong 冲突 | `application/octet-stream` | strong | 否，仅 attachment 下载 |
| 空/通用或匹配文本声明 | HTML/JSON/XML/脚本活动内容 | 检测/规范化声明类型 | none + active risk | 禁止 body CID；仅按强制 `text/plain` 的附件文本预览白名单 |
| 明确非活动媒体声明 | 精确 HTML/SVG/脚本/可执行风险 | `application/octet-stream` | dangerous | 否，仅 attachment 下载 |

兼容关系必须按完整类型维护，不能只比较 `image/*`、`video/*` 等顶级类别。Office Open XML 与 ZIP、Ogg audio/video 与 Ogg 容器等关系要有显式测试。

文件名扩展名永远不是类型证据。原名有扩展时不静默改名；无扩展或 Content-ID 伪文件名时才追加检测扩展。文件名必须剔除路径分隔符、控制字符和 NUL，不能把用户提供的路径写入响应头。

## 7. 下载、预览与正文资源

### 7.1 下载

- 现有 `/attachments/:index` 是兼容下载路由：普通附件保持 `attachment`，历史 inline index 继续保持当前 `inline` disposition；Safe Renderer 不再把该兼容路由当作正文授权判断。
- 新增逻辑 body-resource 路由 `/messages/:message_id/body-resources/:token`（mail-node internal 与 mgmt API 同构代理，实际前缀沿用现有路由层）。token 只用于定位，路由继续执行与正文读取相同的用户/节点鉴权。只有当前 selected branch 中 `Inline=true`、related scope 命中且 `BodyCIDPolicy` 允许的 part 才返回 200/206；不能回退到兼容下载语义。
- `/attachments/:index/preview` 继续只表示独立附件 preview；它与 body-resource 路由不共用大小或 disposition 语义。
- `ConflictStrong`、`ConflictDangerous`、`RiskExecutable` 或未知类型的响应类型为 `application/octet-stream`。只有声明/检测相容的 `RiskActive` 文本可以进入独立附件文本化策略，且响应必须强制 `text/plain; charset=utf-8`。
- 所有二进制响应设置 `X-Content-Type-Options: nosniff`。
- body-resource 成功响应固定 `Content-Disposition: inline` 并使用安全文件名；legacy download/preview disposition 继续按各自 mode 决定。
- 查询、HTTP、DataStream、隔离区使用同一 `PartInfo`，文件名和有效类型不能漂移。

body-resource token 契约冻结如下：

- token 最长 `256` ASCII 字节，使用 base64url 编码的紧凑二进制 payload 和 AES-256-GCM；禁止使用可读 JSON/明文 payload 加 HMAC，因为那会暴露 `PartPath`，不满足 opaque 要求。
- clear prefix 只含 `version/kid`；加密 payload 带 `issued_at/expires_at`，并使用固定 `128-bit` digest 绑定 mailbox stable ID、physical revision fingerprint、`ParserVersion`、`PolicyVersion` 和 canonical `PartPath`。资源请求重新投影当前 eligible body resources 并以 constant-time digest 唯一匹配，0 个或多个候选都按 stale 404；不得为了定位把裸 path 放回 token。固定字段布局必须用最大 depth/path fixture 证明 base64url token 不超过 256 字节。TTL 固定 `1h`，校验允许 `2min` 时钟偏差。
- AEAD key ring 独立于节点注册凭证，持久化在 node identity 目录；目录在 Unix 为 `0700`，key file 为 `0600`，Windows 使用仅服务账号可读的等价 ACL。轮换时只维护 current 和 previous key，previous 固定保留至少 `25h`（1h token TTL + 24h rollback window）。
- mgmt 只透传 token，不得 mint、解密或根据内容分支。invalid authentication tag、过期、未知 key、parser/policy revision 不匹配或 physical revision stale 对外统一返回 404，并只在内部记录有限 stale reason；token 有效但当前策略拒绝时返回 403 `body_resource_denied`。

### 7.2 预览策略

显式白名单：

- 图片：`image/png`、`image/jpeg`、`image/gif`、`image/webp`、`image/avif`、`image/bmp`。
- 视频：`video/mp4`、`video/webm`、`video/ogg`。
- 音频：`audio/mpeg`、`audio/mp4`、`audio/ogg`、`audio/wav`、`audio/flac`。
- 附件独立预览：PDF；`text/*`、JSON/XML/JavaScript/XHTML 及 `+json/+xml` 沿用现有强制 `text/plain; charset=utf-8` 策略，并设置 `nosniff`。

HEIC/MOV/MKV/AVI、SVG、可执行文件、压缩包、strong/dangerous conflict 只下载。HTML 和脚本绝不进入正文媒体元素，但声明/检测相容时可按上一条安全文本化预览，不能作为活动文档返回。

现有 10 MiB 普通附件预览限制保持不变。正文 CID 使用单独的 body-resource 路由：PNG/JPEG/GIF/WebP/AVIF/BMP 图片不增加 preview 专属上限，但仍受第 10 节的单 part 和总 decoded bytes 全局上限；视频和音频还受 `mime.inline_media_max_bytes` 限制。该配置 owner 为 mail-node，默认 `10485760`，范围 `1048576..1073741824`，允许节点覆盖，采用 read-through；必须登记到 mgmt config schema、默认配置、运行时快照和测试。超过限制返回 413，并记录 `preview_denied_reason=too_large`。

HTML `data:` URI 使用独立固定策略：每份 HTML 最多 `32` 项，单项 encoded 最长 `1 MiB`，单项 decoded 最多 `768 KiB`，总 decoded 最多 `2 MiB`。任一项超限只删除该 URI；数量或总量超限后删除其余 `data:` URI 并记录有界诊断。v2.2 不把它复用为 `mime.inline_media_max_bytes`，避免通过 HTML 放大邮件正文。

### 7.3 HTML 引用清洗

邮件正文响应可增加可选 `body_resources[]` 展示投影，不改变 `attachments[]` 成员/index；每项至少包含 `{token, content_id, content_location, filename, size, content_type, media_class, range_supported, external_index?}`，只列出 selected branch/current related scope 中通过后端角色检查的资源。前端构造 typed `ReferenceMap`，同时支持 CID 和当前 scope 的相对 Content-Location，值为 `{downloadUrl?, bodyUrl, previewUrl?, contentType, inline, mediaClass, rangeSupported}`。正文图片、视频和音频只使用由 token 构造的 `bodyUrl`；只有 `external_index` 存在时才构造 download/preview URL。无 external index 的资源播放失败时只显示可读 unavailable fallback，不伪造下载链接。

Content-Location 规范化由前后端共享 fixture 冻结：使用结构化 URL parser，fragment 丢弃、query 保留并精确匹配，percent decode 只执行一次；拒绝 scheme、host、opaque URL、反斜杠和绝对路径。相对引用只相对于 selected HTML part 自身相对 `Content-Location` 的目录解析，`.`/`..` 规范化后不得逃出该相对根；v2 忽略 HTML `<base>` 和 `Content-Base`。CID 仍采用精确优先、唯一 ASCII case-fold fallback。任何歧义都拒绝映射。

允许的映射：

| 元素 | 类型 |
|---|---|
| `img[src]` | 图片白名单 |
| `video[src]`、`video source[src]` | 视频白名单 |
| `video[poster]` | 图片白名单 |
| `audio[src]`、`audio source[src]` | 音频白名单 |

Safe Renderer 固定使用 DOMPurify，先从 detached DOM 删除全部资源承载属性，再只安装 `ReferenceMap` 或已通过固定 `data:` 策略的本地 URL，最后才写入 iframe；不得先插入文档再异步清理。移除 `autoplay`，补 `controls` 和 `preload="metadata"`。删除所有 anchor `href`、`srcset`、远程媒体地址和未命中引用。`data:` 只允许 `data:image/png/jpeg/gif/webp/avif/bmp`，禁止宽泛的 `data:image/*`；SVG 即使是 `data:` 也不映射。前端单测固定使用 Vitest + jsdom，请求安全验收使用 Playwright interception，证明渲染过程没有自动远程请求。

iframe 继续使用 `sandbox="allow-same-origin"`，CSP 至少包含：

```text
default-src 'none';
img-src 'self' data: blob:;
media-src 'self' blob:;
style-src 'unsafe-inline';
font-src data:;
base-uri 'none';
form-action 'none';
```

## 8. Range 与 HEAD

### 8.1 统一字节范围模型

新增纯函数 `ParseSingleRange(header, total)`，支持 `bytes=start-end`、`bytes=start-`、`bytes=-suffix`，拒绝多区间、负数溢出和超过 128 ASCII 字节的 header。非法或越界返回 `416` 与 `Content-Range: bytes */total`。

HTTP 和 DataStream 都使用同一范围结果：`Start`、`End`、`BodyLength`、`RepresentationLength`、`Status`。

HEAD 遵循标准方法语义：忽略请求中的 Range，返回与无 Range GET 相同的 200 表示头和完整 representation `Content-Length`，但不发送 body。v2.2 不产生 ranged HEAD 206。

成功的 body-resource GET 响应冻结以下头：200 带 `Accept-Ranges: bytes` 和完整 `Content-Length`；206 额外带 `Content-Range` 且 `Content-Length` 为范围长度；416 带 `Content-Range: bytes */total`。mgmt proxy 白名单必须加入 `Accept-Ranges`、`Content-Range`，并继续透传 `Cache-Control/Content-Disposition/Content-Length/Content-Type/X-Content-Type-Options`。

### 8.2 typed 请求与 mixed-version

mgmt 内部 `DataRequest` 增加 typed `Method` 和 `Range`，不允许 handler 直接写 metadata 字符串。DataStream 映射到 `DataLocator.OptionsJson`：

```json
{"schema":"attachment_transfer.v1","method":"GET","range":"bytes=0-9","resource_token":"opaque"}
```

- 对 attachment/body-resource 传输请求，`schema` 必填且只能为 `attachment_transfer.v1`；`method` 只能为 `GET` 或 `HEAD`；HEAD 不得携带有效 range。`message.body_resource.v1` 请求必须携带最长 256 ASCII 字节的 `resource_token`，附件 index 请求不得携带该字段。message list 的现有 page/size options 保持原 schema，不套用本白名单。
- `range` 可省略，存在时必须先通过 128 字节和 ASCII 检查；options 总长度不得超过 512 字节；未知字段和未知 schema 返回 400，不静默解释。
- legacy HTTP 的 `legacyRequest` 增加受控 header map，只允许 `Range`，并使用 typed Method 构造 GET/HEAD。
- node-contract 增加字符串请求类型 `message.body_resource.v1`，不修改 protobuf descriptor。新节点声明 `mime.body_resource.v1` 和 `data.range.v1`；mgmt 只有在目标节点具备前者时才生成 body URL，具备后者时才发送 Range/HEAD options。
- 旧节点 Range 请求降级为完整 GET 200，并记录 `range_fallback_legacy_node`；旧节点 HEAD 在发起 DataStream 前返回 501 `head_not_supported`，避免不读取 body 导致流阻塞。
- 同一 capability set 必须同时发布到 control Hello 和 authenticated legacy heartbeat。heartbeat 持久化 capability、`boot_id`、agent version 和 observed timestamp；active dual/control session 以当前 live session 为权威，legacy HTTP 或 fallback 以同 boot ID 的最新有效 heartbeat 为权威。heartbeat `observed_at` 超过 `healthcheck.heartbeat_timeout_seconds`（当前默认 90s）即 stale；missing/stale/boot mismatch 一律按空 capability set 处理。
- `nodetransport.Target` 携带一次请求内不可变的 capability set，或由一个集中 resolver 在请求开始时生成等价 snapshot；各 handler 不得自行猜测。前端只在目标节点具备 `mime.body_resource.v1` 时映射图片 `bodyUrl`，同时具备 `data.range.v1` 时才映射音视频；图片允许 body-resource 完整 200。
- U3 发布前保留旧 renderer 给 non-capable 节点，不允许先全量替换后再等待 capability。启用新 renderer 的覆盖门槛为：active eligible target nodes 在连续 `24h` 观察窗内两项 capability 均达到 `100%`；计划离线或明确豁免节点必须在窗口记录中列出。覆盖不足时新 body route 保持 dark，旧图片行为继续服务。

### 8.3 DataStream HEAD 特殊语义

HTTP HEAD 需要完整的 `Content-Length`，但不发送 body。当前协议不能把这个长度放入 `NodeDataHeader.ContentLength`，否则管理端会拿它和零 chunk 比较并报协议错误。

因此：

1. 扩展 mail-node 内部 `nodedata.Response`，增加 `ProtocolContentLength *int64`。
2. `nil` 保持现有行为：使用 `ContentLength`，并允许当前 dispatcher 从 HTTP `Content-Length` header fallback；这样现有 GET/error/raw constructor 无需同时迁移。
3. 指针值 `-1` 表示显式协议未知/不比较长度，dispatcher 不得再从 headers 反推；HEAD 固定使用该值。指针值 `0` 表示显式零长度协议 body，不能和 `nil` 合并。
4. HEAD 的 HTTP representation `Content-Length` 只放在允许透传的 headers 中。普通 GET/Range constructor 可保持 `nil` 并沿用实际 chunk 长度。
5. DataStream receiver 已具备 `ContentLength=-1` 跳过长度比较的基础语义；补 `nil/-1/0`、普通 GET、HEAD、ErrorResponse、空 body SHA-256/`TotalBytes=0` 回归，不重复改写 receiver 条件。
6. mgmt proxy 对 HEAD 只写状态和 headers，不调用 `io.Copy`。

这不修改 protobuf descriptor，但必须新增 dispatcher、registry、gateway、capability routing、legacy HTTP 和 mgmt proxy 的 HEAD 回归测试。

### 8.4 If-Range

本阶段不声称支持 `If-Range`。mgmt 只透传经过校验的 `Range`，丢弃外部 `If-Range`。未来若增加稳定 ETag，再单独加入 typed 字段和条件范围测试，不能在没有 validator 的情况下写入设计目标。

## 9. 转发 MIME 重写

转发必须使用事务化 raw MIME locator，处理：

- multipart/mixed -> multipart/related -> multipart/alternative 嵌套。
- 折叠 header、LF/CRLF、quoted boundary。
- 精确 boundary delimiter，而不是匹配所有 `--xxx` 行。

语义目标只能来自统一 `ParseResult`：引用位于 selected alternative branch 和当前 related scope，CID 按精确优先/唯一 case-fold fallback 解析，引用元素与 `MediaClass` 匹配，part 声明为空/通用，检测为 strong 且 `ContentRisk=none`。locator 使用 `PartPath` 和该 evidence 定位 raw span，不能扫描所有 HTML 后构造全局 lowercase CID map。

locator 只改目标 part 的 `Content-Type`、`Content-Disposition` 和必要的 `name/filename` 参数，保持 `Content-Transfer-Encoding` 以及 encoded payload 字节完全不变。允许重写的有效类型限于正文媒体白名单，不能因为 `<a href="cid:...">` 或任意字符串引用把脚本、HTML、SVG、压缩包或可执行内容改成 inline。

解析、boundary、重复 header、path 映射或目标数量验证任一失败时，整封原样转发并记录 `mime_rewrite_skipped_reason`，不能产生部分改写。严格 delimiter 允许 RFC trailing transport padding，但不接受前导空白伪 boundary。成功后逐目标计算 encoded payload SHA-256；测试不能只检查 payload substring 仍存在。

顶层 Subject 替换同样属于 raw header 安全边界：替换时必须删除原 Subject 的全部折叠 continuation lines，并保留其他 header 字节；折叠 Subject、重复 Subject 和 LF/CRLF 必须有明确结果或整封原样策略。

## 10. 错误、资源限制与可观测性

至少定义以下稳定 warning：

`mime_parse_failed`、`malformed_child_part`、`missing_boundary`、`charset_decode_failed`、`body_size_limit_exceeded`、`part_count_limit_exceeded`、`mime_depth_limit_exceeded`、`part_size_limit_exceeded`、`decoded_size_limit_exceeded`、`reference_limit_exceeded`、`duplicate_alternative_type`、`missing_related_root`、`related_root_fallback`、`ambiguous_content_id`、`unresolved_cid`、`unreferenced_related_part`、`plain_generated_from_html`、`unsupported_encrypted_body`、`warning_limit_exceeded`。

结果状态和主错误码冻结如下；path 级细节只进入 warnings：

| 条件 | Status / ErrorCode | 查询/正文 JSON | legacy attachment | body-resource / DataStream 等价响应 |
|---|---|---|---|---|
| 完整成功 | `ok` / 空 | 200，现有字段 | 保持现有 200 | 200/206 |
| 可恢复 child/charset/reference 问题 | `partial` / `mime_partial` | 200，保留可用字段和 warnings | 目标 part 可用则保持现有行为 | 目标可用则服务，否则 422 `mime_part_unavailable` |
| text/HTML/reference 字段级上限 | `partial` / `body_size_limit_exceeded` 或 `mime_partial` | 200，省略超限字段/引用，不返回截断内容 | 未超限的既有 external index 保持兼容 | 超限目标 413；未命中引用不服务 |
| 原始 EML 超限 | `too_large` / `message_size_limit_exceeded` | 200，正文/附件派生为空，raw 下载仍可用 | 413，不新增部分内容 | 413 |
| depth/count/single-part/total-decoded 投影超限 | `too_large` / `mime_depth_limit_exceeded`、`part_count_limit_exceeded`、`part_size_limit_exceeded` 或 `decoded_size_limit_exceeded` | 200，不返回截断正文 | 超限目标不得返回 | 413 |
| 不支持的 encrypted body | `partial` / `unsupported_encrypted_body` | 200，无伪造正文 | 已有 external index 的原始 part 仍按兼容策略 | body-resource 415 |
| enmime 致命失败 | `failed` / `mime_parse_failed` | 200，保持可观察 parse status | 保持当前 500 类行为直到版本化迁移 | 422 `mime_unprocessable` |
| recovered parser panic | `failed` / `parser_panic` | 200 且服务端记录计数，不泄露 panic 文本 | 500 | 500 |

同一结果命中多个条件时，Status 优先级为 `failed > too_large > partial > ok`；同一 Status 内主 `ErrorCode` 使用固定优先级：`parser_panic`、`mime_parse_failed`、`message_size_limit_exceeded`、`mime_depth_limit_exceeded`、`part_count_limit_exceeded`、`decoded_size_limit_exceeded`、`part_size_limit_exceeded`、`body_size_limit_exceeded`、`unsupported_encrypted_body`、`mime_partial`。其他条件保留为有界 warnings，不能依赖遍历偶然顺序选择主错误码。

DataStream 返回与对应 HTTP 资源相同的 status/code/header；quarantine key、message lookup、鉴权和 transport failure 保留各自错误语义，不纳入 MIME status 统一。

wire error envelope 保留现有整数 `code` 的类型和含义，只增加可选稳定字符串 `error_code`，不得把 `code` 改成字符串。HTTP 与 DataStream 对同一失败必须返回相同的 status 和 `error_code`：

| 条件 | HTTP status | `error_code` | numeric `code` |
|---|---:|---|---|
| typed options schema/field/长度非法 | 400 | `invalid_transfer_options` | 现有参数类 code |
| Range 语法非法或不可满足 | 416 | `range_not_satisfiable` | 现有 business code |
| token invalid/stale/expired 或资源不存在 | 404 | `body_resource_not_found` | 现有 not-found code |
| token 有效但当前 policy 拒绝 | 403 | `body_resource_denied` | 现有 business code |
| raw EML 超限 | 413 | `message_size_limit_exceeded` | 现有 business code |
| part/total/inline media 超限 | 413 | `body_resource_too_large` | 现有 business code |
| 媒体策略不支持 | 415 | `body_resource_unsupported_media` | 现有 business code |
| MIME part 不可用/整封不可处理 | 422 | `mime_part_unavailable` / `mime_unprocessable` | 现有 business code |
| mixed-version HEAD 不支持 | 501 | `head_not_supported` | 现有 business code |
| internal/transport failure | 现有 status | 现有稳定 internal/transport code | 现有 internal/external code |

限制按生效时点分层：

| 限制 | v2 生效点 | v2 保证 |
|---|---|---|
| 最大原始 EML 字节：`mime.max_message_bytes` | 所有 `ParseSummary/ParseFull/ParseFile` 调用 enmime 前 | 硬拒绝，不向 enmime 提供截断输入；返回 `too_large/message_size_limit_exceeded` |
| 最大投影 part 数 `1000`、最大深度 `64` | enmime 解码完成、projector 遍历前/期间 | 任一超限使整次投影 `too_large`；不保证限制 enmime 已发生的递归和内存开销 |
| 单 part decoded：`mime.max_message_bytes`；总 decoded：overflow-safe `min(2 * mime.max_message_bytes, 2 GiB)` | enmime 解码完成后 | 任一超限使整次投影 `too_large`，enforce 响应不得服务看似可用的 sibling；不保证限制 enmime 已分配的 decoded bytes |
| text body `1 MiB`；HTML body `2 MiB` | projector 字段生成时 | 超限字段为空，结果 `partial/body_size_limit_exceeded`；绝不返回看似完整的截断 HTML |
| CID/Content-Location 最多 `4096` 项，单项最多 `2048` UTF-8 bytes | reference index 构建时 | 超出部分不参与解析，结果 partial 并产生 path warning |
| HTML `data:` URI：`32` 项、单项 encoded `1 MiB`、单项 decoded `768 KiB`、总 decoded `2 MiB` | Safe Renderer 解析时 | 超限 URI 删除；限制值属于 policy version |
| projected warnings 最多 `100` | warning 聚合时 | 有界去重，额外 warning 以一个 `warning_limit_exceeded` 收口；enmime 每 part error 存储仍按 `8` 限制 |

原始 EML 始终保留。depth/count/单 part/总 decoded overflow 是 whole-projection `too_large`，只有 text/HTML/reference/warning 等字段级限制允许 `partial`；不得把截断内容包装成完整 HTML。若安全目标要求 part/depth/decoded bytes 在解码期间硬限制，实施必须暂停并提交 enmime fork/替换 ADR，本设计 v2.2 不包含该能力。

配置契约：`mime.body_projector_mode` 为 mail-node owner 的 `legacy|shadow|enforce` 字符串，默认 `legacy`，允许节点覆盖，read-through；`mime.max_message_bytes` 为 mail-node owner 的整数字节数，默认 `26214400`，范围 `1048576..1073741824`，允许节点覆盖，read-through；`mime.inline_media_max_bytes` 的契约见 7.2 节。三项都必须进入 mgmt config schema、默认配置、节点覆盖、运行时快照和测试。config schema 必须支持 string allowed-values 校验，mail-node 注册 `mailparse.ValidateConfig` apply hook；mode 非三种枚举或任一数值越界时，本 revision 不替换当前 snapshot、不推进 applied revision，并写入 `LastApplyError`。`shadow` 对超限邮件只记录新路径候选结果而仍服务旧输出，`enforce` 才建立解码前硬拒绝；进入 `enforce` 前必须完成 S0 大邮件基线，回滚到 `legacy` 会撤销该新硬限制。

版本契约：v2 首批固定非空 `parser_version` 和 `policy_version`；MIME 树遍历、正文选择或字段投影变化递增 parser version，effective type、inline/preview 白名单或安全展示规则变化递增 policy version。`PartPath -> ExternalIndex` 算法变更必须保持旧 API index 或提供显式迁移对应关系。异步持久化不得先于 `S0/B1/B2/T3/F3/U3/R4` 完成，且不得持久化无版本的派生结果。

可观测性只记录类型和决策，不记录正文或附件字节：

`parser_version`、`policy_version`、`declared_content_type`、`detected_content_type`、`effective_content_type`、`content_type_source`、`detection_strength`、`content_risk`、`conflict`、`preview_denied_reason`、`cid_resolved`、`range_status`、`range_fallback_legacy_node`、`mime_rewrite_skipped_reason`。

Content-ID、文件名和日志字段必须截断并过滤控制字符。

## 11. 验收标准

1. plain only、HTML only、alternative、related `start`、mixed 嵌套、signed、encrypted、report、digest 和 `message/rfc822` 的正文角色、candidate path 与 warning 符合冻结矩阵。
2. 现有 API JSON 和 `Envelope.Attachments + Envelope.Inlines` 的成员、顺序、index 不变；`OtherParts` 不进入 attachments，但 selected related scope 中有效引用的资源可进入可选 `body_resources[]`。
3. 原始 EML 解码前硬限制与 decoded bytes、part size/count/depth 解码后投影限制分别有边界测试，状态/错误映射与配置模式一致。
4. 现有五种 inline 图片和文件名推断测试无回归；octet-stream 的真实 MP4/MP3/Ogg fixture 得到正确类型和后缀。
5. 明确类型 + weak 差异不大面积误降级；明确 strong 冲突不能 preview；HTML/SVG/script/executable 风险不依赖 strong/weak 即拒绝正文媒体。
6. 查询、下载、HTTP preview、隔离区、DataStream 和过滤特征的类型、文件名、正文和 index 决策来自同一 `ParseResult/PartInfo`。
7. HTTP 和 DataStream 的 GET、Range、416、HEAD 行为一致；HEAD 固定 200、忽略 Range 且不断开 DataStream；`ProtocolContentLength` nil/-1/0 和 mixed-version capability source/fallback 均有测试。
8. nested MIME 转发按 selected branch/related scope 只改目标 header，失败零修改，encoded payload SHA-256 不变。
9. HTML 只映射 inline 且类型匹配的 CID/当前 scope 相对 Content-Location；远程 URL、anchor、SVG、HTML、脚本和 mismatch 不进入正文，Playwright interception 证明无自动远程请求。
10. body-resource、legacy download 和独立 preview 权限分离；AEAD token/key rotation/TTL/stale 语义、全局 decoded 上限和 inline media 限制均有边界测试，不改变普通附件的 10 MiB preview 契约。
11. parser/policy version、canonical JSON、warning enum、part path 和 external index mapping 经审核冻结，异步读模型只能消费该版本化契约。
12. mail-node、mgmt-system、node-contract 全量测试与 vet、前端测试/构建通过，并完成 Chromium/Firefox 桌面及移动视口验收。
13. control Hello 与 legacy heartbeat capability 都可解析；live/heartbeat/boot mismatch/stale resolver 行为稳定，active eligible nodes 连续 24h 达到 100% 覆盖前不启用新 renderer。
