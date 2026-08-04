# MIME 正文投影、媒体识别与安全预览实施计划 v2

> 版本：v2.2 | 日期：2026-08-03 | 状态：实施契约已冻结，待执行
>
> 权威性：本文 v2.2 取代历史 MIME v1/v2.0/v2.1 实施计划；旧版本不再作为实施或验收依据。文中的 `*.v1` 若出现，仅表示新 wire contract/capability 自身的首个版本。
>
> 上位设计：[`mime-media-detection-and-safe-preview-design.md`](mime-media-detection-and-safe-preview-design.md)
>
本文是 MIME v2 的唯一实施契约。`S0/B1/B2/T3/F3/U3/R4` 完成前，不实施异步 MIME SQLite schema、正文 Blob 双写或历史回填。契约冻结后按 `S0 -> B1 -> B2` 推进；`B2` 完成后 `T3/F3` 可并行。U3 可基于 B2.2 frozen mocks 提前做 scaffold/单测，但 integration、浏览器验收和启用必须等待 T3 capability path；`R4` 等待 T3/F3/U3。每个依赖边必须有测试证据，未完成的阶段不能在文档中标记为已完成。

## 1. 实施原则

1. 先保存脱敏真实失败样本并证明当前行为，再改解析逻辑和增加能力。
2. 第一阶段保留 `enmime v1.3.0`；只有原始 EML 上限是解码前硬限制，其他结构/decoded 限制是解码后投影准入条件。`BodyProjector` 负责正文与 part 角色，`mimetype` 只负责 decoded bytes 的类型证据。
3. 统一决策入口不等于所有端点使用相同大小限制或相同 Content-Disposition。
4. 普通类型差异只有 strong 冲突才改变有效下载类型；dangerous active/executable 风险独立判定，weak 检测不能绕过 deny，也不能造成大面积兼容回归。
5. Range/HEAD 必须同时覆盖 HTTP、legacy HTTP、DataStream 和 mgmt proxy，并由 `data.range.v1` capability 控制 mixed-version 行为。
6. 转发 raw MIME locator 是强制阶段，只消费统一 scope/path evidence，不以样本未失败为跳过条件。
7. alternative/related、正文选择、CID scope、external index 和 `ParseResult` 均在本计划的 B1/B2 冻结；handler、转发、前端和异步读模型不得另建规则。
8. 保持原始 HTML、现有 API 字段和 attachment index；安全展示结果不进入 canonical parser golden。
9. lookup identity 与 MIME-derived metadata 分离；raw too-large、fatal parse 或 recovered panic 不得把路径已命中的邮件变成 404。
10. 所有常量、错误码、token、capability 来源和降级行为以设计 v2.2 为准，实施窗口内不得再临时选择另一套语义。

## 2. 代码改造地图

### 2.1 mail-node MIME 内核

| 文件 | 改动 |
|---|---|
| `internal/mailparse/types.go` | 增加 `PartPath`、`PartRole`、`BodyView`、`ProjectedPart`、status/error code、parser/policy version，并扩展 `PartInfo`、检测强度、冲突和预览决策模型 |
| `internal/mailparse/tree.go` | 将 enmime 树转为包内遍历视图，生成稳定 path，并先冻结 `Attachments + Inlines` 兼容 index |
| `internal/mailparse/body_projector.go` | mixed/alternative/related/signed/report/digest/embedded 的唯一正文与角色投影规则 |
| `internal/mailparse/reference.go` | Content-ID/Content-Location 规范化、related scope、歧义和反向引用 |
| `internal/mailparse/html_text.go` | 有界结构化 HTML-to-text，替换手写标签状态机 |
| `internal/mailparse/media_detect.go` | 封装检测窗口、调用 `mimetype`、类型强度、ContentRisk 和兼容别名 |
| `internal/mailparse/media_policy.go` | 有效类型、文件名、媒体类别、危险内容和预览原因的唯一决策 |
| `internal/mailparse/parser.go` | `ParseSummary/ParseFull/ParseFile` 共享带固定 options/recover 的 `enmime.NewParser` + projector；附件改用 `ResolvePartInfo`，删除直接正文复制和 `detectImagePart` |
| `internal/mailparse/limits.go` | depth/count/decoded/body/reference/warning 的 v2.2 固定上限与 overflow-safe 聚合 |
| `internal/mailparse/features.go` | 从稳定 `ParseResult` 生成过滤特征，不再次遍历 enmime |
| `internal/mailparse/*_test.go` | MIME 结构、golden、warning、限制、兼容 index、真实头部、冲突和大小边界 |
| `cmd/node/main.go` | router/handler 前调用 `ConfigureDetector`、注册 `mailparse.ValidateConfig`；在 control Hello 与 authenticated legacy heartbeat 发布相同 capability |

### 2.2 响应和传输

| 文件 | 改动 |
|---|---|
| `internal/handler/message_parser.go` | 删除 enmime wrapper，handler 只接收 `mailparse.Result/ProjectedPart` |
| `internal/handler/message_index.go`、`node.go` | 共用 1 MiB 有界 header identity scan；查找不依赖 MIME 派生 Message-ID，稳定处理 fallback physical identity |
| `internal/handler/media_response.go` | legacy download、attachment preview、body-resource 三种响应策略、`nosniff`、Content-Disposition |
| `internal/handler/body_resource_token.go` | 独立持久化 AES-256-GCM key ring、1h TTL、physical revision/parser/policy/path 绑定和统一 stale 映射 |
| `node-contract/contract.go`、测试 | 增加字符串请求类型 `message.body_resource.v1`，不修改 protobuf descriptor |
| `internal/handler/byte_range.go` | 单 Range 解析及 200/206/416/HEAD 结果 |
| `internal/handler/node.go` | HTTP 附件、preview、opaque-token body-resource 和 HEAD 路由使用共享 helper |
| `internal/handler/control_data.go` | DataStream locator options、Range/HEAD 响应 |
| `internal/handler/quarantine.go` | 隔离区使用相同 PartInfo 和响应策略 |
| `internal/nodedata/dispatcher.go` | `ProtocolContentLength *int64` 的 nil/-1/0 三态和 HEAD 禁止 header fallback 语义 |
| `mgmt-system/internal/nodedata/registry.go` | 保持现有 `ContentLength=-1` 语义，补 HEAD 空 body checksum/TotalBytes 回归 |
| `mgmt-system/internal/nodetransport/*.go` | typed Method/Range、`attachment_transfer.v1` options 白名单、legacy 受控 header，以及请求内 immutable capability snapshot |
| `mgmt-system/internal/handler/util.go` | 透传 Range 响应头，HEAD 不复制 body，capability fallback |
| `mgmt-system/internal/handler/email.go` | 注册 body-resource GET/HEAD、生成 capability-aware URL 并读取 typed 请求选项 |
| `mgmt-system/internal/model/model.go`、heartbeat/store 路径 | legacy heartbeat 持久化 capability、boot ID、agent version、observed timestamp，并与 live control session 合并为唯一 resolver |
| `mgmt-system/internal/configschema/schema.go`、默认配置/运行时快照 | 增加 string allowed-values；登记 MIME 三项配置，并保证非法 revision 不替换 snapshot、不推进 applied revision、写 `LastApplyError` |

### 2.3 转发和前端

| 文件 | 改动 |
|---|---|
| `internal/forward/mime_rewriter.go` | 从 `PartPath/scope` evidence 事务化定位 raw header span，只改目标 header |
| `internal/forward/smtp.go` | 删除全局 CID/重复图片检测，调用 locator；正确替换折叠 Subject |
| `internal/forward/*_test.go` | nested multipart、scope/歧义、严格 delimiter、折叠 header、payload SHA-256、零部分改写 |
| `web/src/pages/EmailsPage.jsx` | DOMPurify Safe Renderer、typed CID/Content-Location `ReferenceMap`、图片/视频/音频白名单和旧 renderer capability fallback |
| `web/src/api.js` | 增加 opaque-token body-resource URL 和 capability 字段，不把正文媒体 fetch 成 blob |
| `web/src/App.css` | 稳定媒体容器和移动端布局 |
| `web/package.json`、前端测试配置 | 登记 DOMPurify、Vitest/jsdom、Playwright；增加 sanitizer 单测、请求 interception 和桌面/移动验收脚本 |

## 3. 阶段与完成门槛

### S0：基线与真实 fixture

任务：

- 固定现有 `mailparse`、`handler`、`forward`、`nodedata`、`nodetransport` 测试结果。
- 保存脱敏且最小的真实结构 fixture：plain only、HTML only、alternative、mixed -> related -> alternative、related `start`、signed、encrypted、report、digest、`message/rfc822`、损坏 child、顶层/child 缺 boundary、超深/超多 part，以及 QQ/移动端 octet-stream inline 图片、MP4、MP3/Ogg、强冲突、无文件名和大 inline 图片。
- 增加 Message-ID 正常/缺失/损坏/header 超过 1 MiB、raw too-large 和 enmime fatal fixture，记录 list/detail/raw/resource lookup 基线，证明路径命中不会被 MIME failure 改成 404。
- 检测 fixture 与播放 fixture 分开：前者可以是最小头部，后者必须是浏览器可解码的有效媒体。
- 保存当前 `ParsedMessage`、`Envelope.Attachments/Inlines/OtherParts` 分类、attachment index、handler 响应、filter features 和前端展示基线。
- 统计超过 25 MiB 的现存 EML 与查询调用，作为 raw hard limit 从 shadow 切到 enforce 前的兼容证据。
- 先写目标失败测试，确认失败来自能力缺失而不是 fixture 无效。

门槛：原有测试通过；至少一封真实问题 fixture 稳定复现；目标测试按预期失败且断言指向正文选择、CID scope、媒体决策或安全展示；fixture 不含真实地址、凭证或用户内容。

### B1：稳定树模型与正文投影

任务：

- 新增 `PartPath` 和只在 `mailparse` 内部持有的 enmime adapter；统一使用 `NewParser` + `SkipMalformedParts(true)` + `MaxStoredPartErrors(8)` + `DisableTextConversion(true)`，并在入口设置普通 panic recover。计算角色前保存 enmime `Attachments + Inlines` 的成员和顺序，`OtherParts` 的 `ExternalIndex` 恒为 nil。
- 实现 1 MiB 有界 RFC 5322 header `MessageIdentity` scan，统一 list/detail/raw/attachment/body-resource lookup；physical identity 忽略 `new/cur` 和 Maildir flags，physical revision 绑定 size/mtime/raw hash。
- 新 projector 路径的所有 `ParseSummary/ParseFull/ParseFile` 在调用 enmime 前执行相同 raw EML 上限；`shadow` 只记录候选 `too_large`，`enforce` 才阻断旧输出，绝不向 enmime 传入截断内容。
- 实现 v2.2 固定上限：depth 64、projected parts 1000、单 part decoded=`mime.max_message_bytes`、总 decoded=`min(2*max,2 GiB)`（overflow-safe）、text 1 MiB、HTML 2 MiB、reference 4096 项/单项 2048 bytes、projected warnings 100；测试和日志必须注明其不限制 enmime 已发生的内存或递归开销。
- 实现 `Content-Disposition: attachment` 优先、alternative 从后向前选择、related root/scope、mixed primary/supporting，以及 signed/encrypted/report/digest/message-rfc822 固定角色。
- 使用结构化 HTML parser 生成兼容 text；`ParseSummary/ParseFull/ParseFile` 共享一次 decode/project，summary 只控制字段投影，不改变 MIME 语义。
- 增加有限 warning、`ParseStatus/ErrorCode/ParserVersion/PolicyVersion` 和 canonical JSON；skipped child error 只绑定最近保留父 path，warning/path 不泄露内容；root fatal、child partial、parser panic 映射按设计冻结。
- 登记 `mime.body_projector_mode=legacy|shadow|enforce`（默认 `legacy`）和 `mime.max_message_bytes`（默认 25 MiB、范围 1 MiB..1 GiB）：mail-node owner、允许节点覆盖、read-through，并进入 schema、默认配置、运行时快照和测试。增加 allowed-values 与 mail-node apply hook；非法 revision 不替换 current snapshot、不推进 applied revision，写 `LastApplyError`。

门槛：设计的 MIME 结构矩阵全部通过；普通 fixture 的 `text_body/html_body` 无无意回归；attachments 成员与 index 和当前 `Attachments + Inlines` 完全一致并可反查唯一 path；`OtherParts` 不新增旧下载项；raw too-large/fatal/panic lookup 仍返回 200 parse status 而非 404；三种 mode 和非法配置 apply 测试通过；`mailparse.Result` golden 稳定，handler 不新增 MIME 树规则。

### B2.1：检测器和决策内核

任务：

- 将当前间接依赖的 `mimetype` 固定为经过项目测试和依赖审计的直接依赖版本，并记录最终 hash；不以“最新版本”作为自动升级条件。
- 实现 `ConfigureDetector`，用 `sync.Once` 设置 64 KiB，并由 `cmd/node` 在 router/handler 前显式调用；增加 3072 字节之后签名和 Gin validator 回归测试。
- 实现 normalized declared type、detected type、DetectionStrength、ContentRisk、兼容别名和媒体类别；危险活动内容判断独立于 strong/weak。
- 实现 CID 精确优先、唯一 case-fold fallback 和歧义拒绝；Content-Location 使用结构化 URL parser、single percent decode、fragment 丢弃和相对根防逃逸，忽略 `<base>`/`Content-Base`；related 引用只能解析当前 selected branch/current scope，绝对/远程 location 阻断。
- 仅 generic declaration 使用 strong/weak detection 覆盖；明确声明只在 strong conflict 时降级。
- 文件名只由 MIME filename/CID/检测扩展补全，不以扩展名反推类型；过滤路径和控制字符。
- 生成 `ExternalIndex -> PartPath -> PartInfo` 唯一映射，以及独立的 `BodyResource -> PartPath -> PartInfo` 投影；后者可引用有效 `OtherParts`，但不能改变 attachments 成员/index。
- `Attachments`、filter features、handler、隔离区和 DataStream 全部调用同一入口。

门槛：五种旧图片、AVIF/HEIC、MP4/WebM/Ogg、MP3/WAV/FLAC、PDF/ZIP/Office、文本、未知、弱差异、strong 冲突和精确 HTML/SVG/declared-script/executable dangerous conflict 测试通过；测试先断言 mimetype 的真实结果，不能把 printable text 硬标为 script。

### B2.2：统一下载/预览/body-resource 策略

任务：

- 新增 `PartResponsePolicy(info, mode)`，区分 legacy download、attachment preview、body resource 三种 mode；legacy route 保持现有 inline disposition 兼容，Safe Renderer 只使用 body-resource route。
- 实现最长 256 ASCII 字节的 AES-256-GCM opaque token 和独立持久化 key ring：clear prefix 只有 version/kid，加密 claim 用固定 128-bit digest 绑定 mailbox/physical revision/ParserVersion/PolicyVersion/PartPath，reprojection 后 constant-time 唯一匹配；用最大 path fixture 证明长度上限。TTL 1h、skew 2min，key file 使用 owner-only 权限，previous key 至少保留 25h，mgmt 不 mint/decrypt。invalid/stale 对外 404，valid-but-denied 为 403。
- 注册 `/messages/:message_id/body-resources/:token` 逻辑路由及 mgmt 对应代理；新增可选 `body_resources[]` 展示投影，不改变 attachments 成员/index。普通附件 preview 继续使用现有 10 MiB 限制；raster body resource 无 preview 专属上限但受全局 decoded 限制，video/audio 还使用 `mime.inline_media_max_bytes`（默认 10 MiB）。
- 在 config schema、默认配置、节点覆盖、运行时快照和测试登记该 key：范围 1 MiB..1 GiB、mail-node owner、read-through。
- mismatch/unknown 只能 attachment + octet-stream；所有二进制响应加 `nosniff`。
- HTTP、隔离区和 DataStream 的 Content-Type、文件名及媒体策略产生的 403/413/415 code 一致；保留现有整数 `code` 并增加可选稳定字符串 `error_code`，资源 lookup、quarantine 和 transport 错误保留各自语义。
- 为 body-resource HEAD 注册 route，并确认 Gin 不写 body；HEAD 忽略 Range 且固定返回完整表示的 200 headers。

门槛：同一 EML 在查询、legacy download、preview、body-resource、隔离区、DataStream 获得相同 PartInfo；`OtherParts` 中被有效引用的资源可通过 token 使用但不进入 attachments；三种 mode 的 disposition/大小行为分别稳定。

### T3：单 Range 与 HEAD 协议

任务：

- 实现 `ParseSingleRange`：prefix/open-ended/suffix、溢出、空值、越界、多区间、128 ASCII 字节上限。
- 普通 GET：200；合法 Range：206；非法/越界：416 + `Content-Range: bytes */total`。
- 200/206/416 冻结 `Accept-Ranges/Content-Range/Content-Length`；mgmt proxy 明确加入响应 header 白名单。
- 扩展 mail-node `nodedata.Response.ProtocolContentLength *int64`：nil 保持旧行为，pointer -1 显式未知且禁止 header fallback，pointer 0 显式零长度；HEAD 用 pointer -1，HTTP representation `Content-Length` 保存在 headers。
- 修改 dispatcher 仅在字段为 nil 时允许旧 header fallback；覆盖 nil/-1/0、GET、HEAD、ErrorResponse constructor。
- 保持 mgmt registry 现有 `-1` 跳过长度比较逻辑；新增空 body SHA-256 和 `TotalBytes=0` 测试。
- `DataRequest` 增加 typed Method/Range/BodyResourceToken；DataStream 只编码 `attachment_transfer.v1` schema，`message.body_resource.v1` 强制 token，legacy HTTP 只允许 Range header；未知字段/schema、超长 options 返回 400。
- 在 control Hello 和 authenticated legacy heartbeat 同时发布 `mime.body_resource.v1`、`data.range.v1`；heartbeat 持久化 boot ID/agent version/timestamp。集中 resolver 以 live session 优先、同 boot heartbeat fallback；超过 `healthcheck.heartbeat_timeout_seconds`（默认 90s）、mismatch 或 missing 为空，并把 immutable snapshot 放入 transport target。
- 无 body-resource capability 不生成新 URL并继续使用旧 renderer；旧节点 Range 降级完整 200 并计数，旧节点 HEAD 在启动流前返回 501；HEAD 只写 headers，不 `io.Copy`。
- 本阶段明确丢弃外部 `If-Range`，不虚构 ETag/Last-Modified 支持。

门槛：HTTP 和 DataStream 对 GET/Range/416/HEAD 状态、headers、body 一致；HEAD 固定 200 且无 chunk，不触发 `ErrProtocol`；control、legacy heartbeat、boot mismatch、stale 和无 capability 路径均有测试。

### F3：事务化 raw MIME 转发定位与重写

任务：

- 使用根 Content-Type 的真实 boundary 递归定位 part，严格识别 delimiter 和 closing delimiter。
- 支持折叠 header、LF/CRLF、quoted boundary 和三层 nested multipart。
- 只消费 selected branch/current related scope 的 `PartPath + CID/media` evidence；精确 CID 优先，唯一 case-fold fallback，歧义拒绝。
- 只在引用元素与安全媒体类型匹配、声明 generic、检测 strong、`ContentRisk=none` 时修改目标 header；任意 cid 字符串或 `<a>` 引用不构成媒体 evidence。
- 只修改 header 字节区；保留 transfer encoding，并对每个目标 encoded payload 计算修改前后 SHA-256。
- boundary/header/path/重复目标任一失败时零修改、原样转发并记录有限 `mime_rewrite_skipped_reason`，不能让 SMTP 投递失败。
- delimiter 不接受前导空白伪 boundary；顶层折叠 Subject 替换必须删除全部 continuation lines，重复 Subject 有明确失败策略。
- 删除 `detectForwardImage`、`shouldAppendForwardExtension` 和全局 boundary 正则。

门槛：真实 nested fixture、duplicate/cross-scope CID、未选择 alternative、前导空白伪 boundary、base64、quoted-printable、CRLF/LF 均通过；失败 fixture 整封零修改，成功 fixture 的 encoded payload SHA-256 不变。

### U3：管理端安全媒体

任务：

- B2.2 contract 冻结后可基于 frozen mocks 开始组件 scaffold 和 sanitizer 单测；接真实 API、浏览器验收及 rollout 必须等待 T3 capability resolver/transport 完成。
- typed `ReferenceMap` 从可选 `body_resources[]` 构建，同时索引 CID 和当前 scope 相对 Content-Location，保存 `{downloadUrl?,bodyUrl,previewUrl?,contentType,inline,mediaClass,rangeSupported}`；所有正文媒体使用 opaque-token body URL，只有 `external_index` 存在时才创建 download/preview URL。
- 使用 DOMPurify 在 detached DOM 中先删除全部资源承载属性和 anchor `href`，再只安装类型匹配的 `img/video/audio/source/poster` 本地 URL，最后写入 iframe；删除 autoplay、远程 URL、srcset，不允许先插入再清理。
- `data:` 只允许 raster 图片，固定 32 项、单项 encoded 1 MiB、decoded 768 KiB、总 decoded 2 MiB；SVG/HTML/script/mismatch 不进入正文。
- CSP 增加 `media-src`，不增加 script 权限；保留 sandbox。
- 视频/音频仅在 `rangeSupported=true` 时映射，直接使用 body URL，不能先 `fetch -> blob`。
- 前端播放失败时：有 `external_index` 才保留下载入口；否则只显示可读 unavailable fallback，不生成伪下载链接。
- 增加 DOMPurify、Vitest/jsdom 和 Playwright 依赖/脚本。jsdom 使用显式 ResourceLoader/请求探针，Playwright 使用 request interception，证明远程 URL、anchor 和类型错配没有请求；不能只断言输出字符串。

门槛：capable 节点的 CID/Content-Location 图片无回归，non-capable 节点继续旧 renderer；MP4/WebM/MP3/Ogg 有 controls、metadata、无 autoplay；类型错配和远程 URL 零请求；有/无 external index 的 fallback 正确；移动端无溢出。U3 integration 门槛不能用 B2 mocks 代替 T3 实链路证据。

### R4：全链路验收与发布

任务：

- 执行 node-contract、mail-node、mgmt-system 全量 Go 测试和前端测试/构建。
- 删除 handler 中直接导入 enmime 的运行时路径；过滤特征从同一 `ParseResult` 投影并验证现有 filter golden 不漂移。
- 冻结 `ParseResult` canonical JSON、warning enum、part path、external index、`ParseStatus/ErrorCode` 到 HTTP/DataStream 的映射，以及 parser/policy version 递增规则。
- 对现有按需列表、正文、附件、隔离区和 DataStream 链路执行集成测试；记录 shadow compare 字段供异步项目使用，但不创建数据库表。
- Chromium/Firefox 桌面及 390x844、412x915 视口验收。
- 观察识别分布、strong conflict、413/415、未解析 CID、Range 416 和转发跳过计数。
- 发布顺序：支持 `mime.body_resource.v1`、`data.range.v1` 的 mail-node -> control + legacy heartbeat capability -> mgmt 集中 resolver/routing -> active eligible nodes 连续 24h 两项 capability 100% 覆盖门槛 -> 前端新 renderer。
- 目标节点缺少 `mime.body_resource.v1` 时不生成任何新 body URL；只有缺少 `data.range.v1` 时图片仍可使用 body-resource 完整 200、音视频不映射。Range fallback 和 HEAD 501 必须可观测。

门槛：`rg "enmime" internal/handler` 无运行时代码命中；全部结构、媒体、传输和安全矩阵通过；版本化契约经审核冻结；capability 发布、前端 gate 和回滚演练均有证据。

## 4. 共享实施 DAG

```text
Contract freeze
    |
    v
S0 事实基线/真实 fixture
    |
    v
B1 parser/tree/identity/limits
    |
    v
B2.1 resolver/reference ----> B2.2 response/token
    |                             |
    +-------------+---------------+
                  |
          +-------+-------+
          |               |
          v               v
      T3 transport      F3 forwarding
          |
          v
      U3 integration
          |
          +------- F3 ----+
                  |
                  v
              R4 release
                  |
                  v
          A5 async read model
```

图中的 B2.1/B2.2 可以在接口冻结后由不同提交推进，但 B2 总门槛必须完整通过。F3 与 T3 在 B2 后并行；U3 scaffold/mock 单测可在 B2.2 后提前进行，U3 integration/acceptance/rollout 必须等待 T3。R4 必须同时等待 T3/F3/U3。`A5` 才允许 ParseCoordinator、SQLite shadow schema、正文与 inline Blob 双读；随后再评估普通附件 `all_parts`。正文语义差异必须回到 projector/parser version 解决，不能在持久化查询层增加特例。

## 5. 测试矩阵

### MIME 结构与兼容

| 类别 | 最低覆盖 |
|---|---|
| 容器 | mixed、alternative、related、signed、encrypted、report、digest |
| 单 part | plain、HTML、generic binary、message/rfc822 |
| 字符集 | UTF-8、GB18030、UTF-16、未知声明和错误声明 |
| 传输编码 | identity、base64、quoted-printable、malformed partial |
| 引用 | exact CID、URL encoded once、case fallback、duplicate、cross-scope、Content-Location relative base/escape/absolute reject |
| 限制 | raw EML 解码前硬拒绝；decoded bytes、part size/count/depth 解码后投影拒绝；warning count |
| 兼容 | fallback Message-ID、`Attachments + Inlines` 成员/index、`OtherParts` 不进入 attachments 但可按有效引用进入可选 `body_resources[]`、filter golden、API JSON |

### MIME 决策

| 场景 | 预期 |
|---|---|
| generic + PNG/MP4/MP3 | detected effective，补后缀 |
| explicit + same/alias | declared effective，无冲突 |
| explicit image/png + ZIP | strong conflict，octet，禁止 preview |
| explicit image/png + 精确 HTML/SVG 或 declared script | dangerous conflict，不依赖 strength，octet，禁止 preview/body resource |
| explicit text/csv + text/plain | weak difference，不降级 |
| generic/匹配 HTML 或脚本 | RiskActive；正文资源拒绝，独立 preview 强制 text/plain + nosniff |
| Office Open XML + ZIP evidence | compatibility，不误报冲突 |
| 空/短/未知内容 | fallback，不影响整封解析 |
| 文件名含路径/控制字符 | 清理后响应 |

### 传输

| 请求 | HTTP/DataStream |
|---|---|
| GET | 200 + 完整 body |
| `bytes=0-9` | 206 + 10 bytes |
| `bytes=10-` | 206 |
| `bytes=-10` | 206 |
| 多区间/越界 | 416 + `bytes */total` |
| HEAD（含 Range header） | 忽略 Range，200 完整表示 headers，无 chunk，无协议错误 |
| Range + 旧节点无 capability | 完整 200 + fallback 指标 |
| HEAD + 旧节点无 capability | 流启动前 501 `head_not_supported` |

### 前端和转发

- CID/Content-Location 图片、视频、音频和 poster 在 selected alternative/current related scope 内严格匹配，并可覆盖无 external index 的 `OtherParts` body resource。
- 外部 URL、anchor href、SVG、HTML、脚本、mismatch、未命中引用不发请求；浏览器 interception 记录必须为空。
- nested MIME 只改目标 header，encoded payload hash 不变。
- 普通未引用附件和明确正确声明的 part 不改写。

## 6. 验证命令

```powershell
go test -C node-contract ./...
go test -C mail-node ./internal/mailparse ./internal/handler ./internal/forward ./internal/nodedata
go test -C mail-node ./...
go vet -C mail-node ./...
go test -C mgmt-system ./internal/nodetransport ./internal/nodedata ./internal/handler
go test -C mgmt-system ./...
go vet -C mgmt-system ./...
npm --prefix mgmt-system/web test
npm --prefix mgmt-system/web run build
```

浏览器验收必须另行记录 Chromium/Firefox、桌面与 `390x844`、`412x915` 视口结果，以及 fixture 对应的网络请求清单。

## 7. 实施窗口 Runbook

### 7.1 窗口准入

窗口负责人在开始改代码前逐项签字，任一项缺失则不进入 B1：

- 本设计与实施计划 v2.2 已冻结，所有参与者确认不再使用 MIME v1/v2.0/v2.1 文档。
- S0 fixture、baseline manifest、现有测试结果和超过 25 MiB 邮件统计已归档；fixture 已脱敏且能稳定复现目标差异。
- B1/B2、T3、F3、U3、R4 owner 与 reviewer 已指定，跨仓/前后端接口 owner 唯一。
- `node-contract`、`mail-node`、`mgmt-system` 全量 Go 测试，前端测试和 production build 在窗口基线提交上全绿；结果包含 commit、命令、时间和 artifact 路径。
- `legacy` 配置回切、body route dark、转发原样 fallback、前端上一构建回退的实际命令已在非生产环境演练并记录，不只写文字预案。
- 原始 EML、fixture 和 golden 已确认不会被 rollout/rollback 删除或覆盖。

### 7.2 阶段执行与交接证据

| 阶段 | 必须产出的退出证据 | 下一阶段 |
|---|---|---|
| S0 confirm | baseline manifest、失败前测试、lookup/attachment/filter/forward/frontend 快照 | B1 |
| B1 | parser options、identity、limits、status/golden、legacy/shadow/enforce 与非法 config apply 测试 | B2.1 |
| B2.1 | MIME/reference/type/risk 矩阵，唯一 ExternalIndex/BodyResource 映射 | B2.2 |
| B2.2 | 三种 response mode、AEAD token/key rotation/stale、error envelope、body-resource GET/HEAD 测试 | T3 与 F3 |
| T3 | HTTP/legacy/DataStream/mgmt 的 nil/-1/0、GET/Range/416/HEAD 和 capability source/fallback 测试 | U3 integration |
| F3 | nested raw locator、失败零修改、成功 payload SHA-256 不变 | R4（与 U3/T3 汇合） |
| U3 | DOMPurify/jsdom 单测、Playwright 零远程请求、capable/non-capable、桌面/移动证据 | R4 |
| R4 | 全量 test/vet/build、24h capability coverage、shadow 指标、回滚演练与发布审批 | A5 或关闭窗口 |

每一行必须由 owner 提交 evidence bundle、reviewer 确认门槛后才能交接。T3/F3 可并行，但失败互不豁免；U3 mock 通过不等于 U3 integration 通过。配置只按 `legacy -> shadow -> enforce` 推进，且每次切换都记录 revision、节点覆盖率、观察时长和回切点。

### 7.3 立即停止条件

出现以下任一情况，停止当前 rollout，不继续扩大节点或前端覆盖：

- attachment 成员/index、现有 API JSON 或 filter feature 发生未解释漂移。
- parser panic 计数非零，或 raw too-large/enmime fatal 从可观察 200 状态退化为 404。
- F3 成功路径的任一 encoded payload SHA-256 变化，或失败路径产生部分 MIME 修改。
- Safe Renderer/浏览器 interception 观察到任何意外远程请求、anchor 导航或 active content 执行。
- HEAD 产生 DataStream `ErrProtocol`、非零 chunk、错误 `ContentLength` 推导，或 HTTP/DataStream status/header 不一致。
- non-capable/legacy 节点出现现有 CID 图片回归，或 capability boot/stale 来源无法解释。
- 原始 EML、fixture 或 golden 有被覆盖/删除风险。

停止后先保全请求、revision、fixture、日志和 metrics 证据，再按第 8 节回滚。不得通过放宽 sanitizer、伪造 capability、改变 attachment index 或删除失败样本继续上线。

### 7.4 窗口退出

窗口只允许两种结束状态：R4 全部门槛通过并留下发布记录，或所有已启用行为显式回滚并留下未完成阶段清单。不得保留“新 U3 已启用但 capability/transport 未完成”的半启用状态；未完成项保留在原阶段，下个窗口从其准入门槛重新验证。

## 8. 回滚

- B1/B2 使用 `mime.body_projector_mode` 进行 old/new shadow compare；回滚切回 `legacy` 输出，不删除 fixture/golden，并明确 raw hard limit 会一并撤销。
- T3/body-resource route 可以保留已部署代码，但 capability resolver 必须返回空集或保持 route dark，禁止新 URL 继续生成。
- F3 任一定位/验证异常立即跳过 rewrite 并转发原始 EML，不把重写失败升级为 SMTP 投递失败。
- U3 回滚到上一 renderer/build，直到 capability 覆盖门槛重新满足；不得放宽当前 sandbox/CSP。正文媒体受 `mime.body_resource.v1` gate 控制，视频/音频还受 `data.range.v1` gate 控制。
- parser/policy version 不原地覆盖；异步项目尚未开始时不产生数据迁移。
- 任一阶段发现 attachment index、现有 API 或 filter feature 漂移，停止推进并先修复兼容映射。
- 原始 EML 始终保留；任何 projector 或 raw locator 错误不得要求用派生正文重建原件。

## 9. 完成定义

`S0/B1/B2/T3/F3/U3/R4` 均有失败前/成功后证据，真实问题 fixture 已修复，全量测试、vet 和浏览器安全验收完成，HEAD/Range 两条传输路径一致，事务化 raw locator、capability 发布、前端 gate 和回滚演练全部完成后，本计划才可标记“已完成”。仅普通 happy-path 邮件通过、仅更换解析库、仅修复一个魔数或仅让前端显示出来，都不构成完成。
