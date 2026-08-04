# 邮件异步 MIME 解析与持久化读模型设计

> 版本：v1.0 | 日期：2026-08-03 | 状态：待实施
>
> 适用范围：`mail-node` 收信后处理、MIME 解析、邮件列表/正文、inline 资源、附件下载、过滤特征与派生数据生命周期。
>
> 关联设计：[MIME 正文投影、媒体识别与安全预览](mime-media-detection-and-safe-preview-design.md)、[Maildir 邮件路径索引](maildir-message-index-design.md)、[附件下载](attachment-download-design.md)、[部署容量与附件存储边界](../deployment-capacity.md)。

## 1. 结论

**实施优先级前置条件：**正文 MIME 投影实施计划 `S0/B1/B2/T3/F3/U3/R4`、真实问题 fixture、`ParseResult/PartPath/warning` 和 parser/policy version 契约完成前，本设计只允许做指标基线和 ParseCoordinator 原型，不得创建最终 SQLite schema、双写正文/part Blob 或启动历史回填。持久化只能复用已经验证正确的解析结果，不能成为修复正文语义的地方。

当前查询链路以 Maildir EML 为唯一内容来源，列表、正文和每个附件/inline 请求都可能调用 `enmime.ReadEnvelope`。路径索引已经消除了按 Message-ID 查找时逐封完整解析的问题，但没有消除目标 EML 的重复全量解码：一封包含多张 CID 图片的邮件会在详情请求和每个图片请求中被重复解析。

目标架构采用以下边界：

1. **Maildir EML 是唯一原件事实源**。解析结果、数据库记录和 Blob 都是可重建的派生数据。
2. **SMTP 投递不等待完整 MIME 解析**。邮件完成 Maildir 原子投递后才进入解析任务，解析故障不得改变 SMTP 已成功接收的事实。
3. **每个物理邮件版本只完整解析一次**。过滤特征、查询模型、正文和 MIME part 元数据由同一 `ParseResult` 投影，消费者不得各自重新解释 EML。
4. **小数据与大字节分离**。节点本地 SQLite/WAL 保存任务和元数据；正文、inline 和附件字节通过 `BlobStore` 保存。首期使用节点本地文件实现，后续可以切换到托管 OSS/S3 或满足独立部署容量条件的 MinIO。
5. **查询优先读持久化读模型**。迁移期缺失、过期或解析失败时保留当前按需解析兜底；原 API 路径和 JSON 字段不变。
6. **持久化不等于无限缓存**。所有队列、并发、正文、part、重试、Blob 容量和保留期都有明确上限。

该方案不把中心 `mgmt-system` 或中心 MySQL 放入节点收信的关键路径，也不要求为首期部署 Redis、Kafka、RabbitMQ 或 MinIO。

## 2. 当前事实与问题

### 2.1 当前调用成本

| 场景 | 当前行为 | 主要成本 |
|------|----------|----------|
| 收信过滤 | legacy 模式读取有界正文预览；非 legacy 模式执行完整 `ParseFile` | 非 legacy 已产生一次全量解析，但结果未供查询长期复用 |
| 邮件列表 | 扫描并排序邮箱文件，对当前页逐封 `ParseSummary` | `ParseSummary` 仍调用 `enmime.ReadEnvelope`，附件也会解码 |
| 邮件详情 | 路径索引定位目标后执行 `ParseFull` | 每次详情请求重新完整解析目标 EML |
| inline 图片 | 每个 CID URL 单独打开并解析同一 EML | 一封邮件的多个图片请求可能并发放大 CPU、RSS 和 GC |
| 附件下载/预览 | 每次重新解析 EML，再取指定 `part.Content` | 首字节必须等待整封 MIME 解码；无法获得真正对象级 Range |
| 节点重启 | 进程内路径索引和任何临时结果丢失 | 首次查询冷扫描；热点邮件重新解析 |

列表默认每页 20 封、最大 100 封。当前代码没有目标 Linux 机器上的 p50/p95、峰值 RSS 和 GC 基准，因此本文只定义可验证的性能目标，不承诺固定 QPS。

### 2.2 不能只加内存缓存

进程内解析缓存可以降低热点邮件的重复解析，但不能成为最终数据边界：

- 节点重启后全部失效；
- 缓存解码后的大附件会形成不可控 RSS；
- 无法为列表提供持久化排序和分页索引；
- 无法提供 Blob 级 Range、ETag、去重和生命周期；
- 多实例或未来迁移对象存储时没有稳定数据契约。

内存缓存仍可作为读模型之上的有界加速层，但不能作为解析完成状态或附件存在性的事实源。

## 3. 目标与非目标

### 3.1 目标

1. 同一个物理 EML 版本在正常路径只执行一次完整 MIME 解析。
2. 邮件列表不再为当前页解码正文和附件；列表成本主要由 SQLite 索引查询决定。
3. 正文、inline 图片和附件读取不再触发整封 EML 重解析。
4. 解析任务可重试、可恢复、可限流，mail-node 重启后不会丢失待处理状态。
5. 节点与中心控制面断连时仍能完成投递、解析和本地数据读取。
6. 保持现有 Message-ID、fallback ID、附件 index、鉴权和 HTTP/DataStream 契约。
7. 支持解析器升级后的按需重建、后台回填、双读比对和快速回滚。
8. 删除、Maildir `new -> cur`、隔离、恢复和保留期清理都有明确的派生数据处理规则。

### 3.2 非目标

- 不把解析放进 Postfix SMTP 接收事务，不因解析失败拒收已满足 SMTP 策略的邮件。
- 不用数据库或 Blob 替代原始 EML，也不允许根据派生数据重写原件。
- 不在本阶段实现全文搜索、语义索引、OCR、病毒扫描、媒体转码或 DLP。
- 不保证所有损坏 MIME 都能解析；损坏原件必须保留并暴露明确状态。
- 不自动加载邮件中的远程图片，不放宽现有 HTML sandbox、CSP 和媒体白名单。
- 不在 mail-node 上混部生产 MinIO；自建 MinIO 的启用条件沿用容量文档。托管 OSS 不受本地 MinIO 的硬件基线约束，但必须单独评估网络、费用、地域、凭据和服务可用性。
- 不承诺数据库与 Maildir 的分布式强事务；一致性由本地事务、幂等任务和补偿扫描保证。

## 4. 事实源与职责边界

### 4.1 数据所有权

| 数据 | 所有者 | 是否事实源 | 丢失后的处理 |
|------|--------|------------|----------------|
| 原始 EML | Maildir/Postfix/Dovecot | 是 | 不能由读模型恢复；按邮件备份策略处理 |
| 邮件路径和文件指纹 | mail-node | 否 | 扫描 Maildir 重建 |
| 解析任务、邮件元数据、part 元数据 | mail-node 本地 SQLite | 否 | 从 EML 重建 |
| 正文、inline、附件 Blob | `BlobStore` | 否 | 从 EML 重建；缺失时不得伪造成功 |
| 邮箱、域名、节点和策略配置 | mgmt-system/MySQL | 是控制面事实源 | 使用现有配置快照和断连策略 |
| HTML 展示安全决策 | mail-node 媒体策略 + 管理端 sandbox | 否 | 使用代码版本和测试重建，不写入原件 |

SQLite 和本地 Blob 根目录必须位于 mail-node 自有数据目录，不能写入 Maildir 的 `new/cur/tmp`，不能被 Dovecot 当作邮件文件扫描。

### 4.2 组件职责

| 组件 | 负责 | 不负责 |
|------|------|--------|
| Postfix/Dovecot | SMTP 接收、Maildir 原子投递、IMAP 状态迁移 | MIME 读模型、对象提取 |
| Maildir 发现器 | 发现稳定 EML、更新路径、创建幂等任务 | 解析正文、执行 HTTP 请求 |
| ParseCoordinator | 去重、限流、调用解析器、提交单一解析修订 | 业务过滤规则、前端清洗 |
| `mailparse` | MIME 树、字符集、正文、part、媒体证据和过滤特征 | 数据库、Blob、HTTP、重试 |
| MetadataStore | 任务状态、解析修订、列表索引、part 映射 | 保存大附件字节 |
| BlobStore | 不透明字节、checksum、长度、Range 能力 | Message-ID 语义、鉴权、邮件生命周期决策 |
| 查询服务 | 校验原件状态、读取活跃修订、兼容回退 | 再次解释 MIME 类型 |
| mgmt-system | 鉴权、节点路由、控制通道和响应代理 | 保存节点邮件正文或成为收信依赖 |
| Web 前端 | sandbox、CID 元素映射、远程资源阻断 | 判断附件真实类型、直接访问未鉴权对象 |

`enmime.Envelope` 和 `enmime.Part` 不得越过 `mailparse`/解析协调器边界进入 handler。跨层只传递稳定的内部 `ParseResult` 和 Blob locator，防止不同 handler 再次实现 MIME 规则。

## 5. 总体架构

```text
Postfix/Dovecot
      |
      | Maildir 原子投递完成
      v
Maildir EML ---------------> 原件读取 / raw EML 下载
      |
      v
发现器 -> SQLite parse_jobs -> 有界 ParseCoordinator
                                  |
                                  v
                              mailparse
                           /       |       \
                          v        v        v
                    过滤特征   元数据投影   Blob 写入
                          \        |        /
                           \       v       /
                            -> SQLite 修订提交
                                      |
                          +-----------+-----------+
                          |                       |
                          v                       v
                    列表/正文 API           inline/附件 API
                          |                       |
                          +------ mgmt-system ----+
                                      |
                                   浏览器/API
```

完整解析发生在 mail-node 内部，不把 EML 字节发送到 mgmt-system 后再解析。中心控制面不可用不会阻止本地任务消费；需要上报的过滤结果继续通过现有 outbox/control stream 最终一致发送。

## 6. 邮件身份、版本与幂等

### 6.1 内部物理身份

RFC `Message-ID` 可能缺失或重复，不能作为物理邮件主键。新增内部 `message_key`：

```text
message_key = SHA-256(
  node_uuid + "\0" + canonical_mailbox + "\0" + canonical_maildir_unique_name
)
```

`canonical_maildir_unique_name` 去除 `:2,flags`，使同一文件从 `new/` rename 到 `cur/` 后身份不变。节点 UUID 防止不同节点生成冲突 key。

边界：

- 外部 API 继续使用现有 `message_id`，不得暴露 `message_key`。
- 同邮箱内重复 `Message-ID` 仍按当前排序和匹配语义处理；读模型必须保存物理 key，不能覆盖另一封原件。
- 缺失 Message-ID 时，迁移期继续返回现有 fallback ID。新旧 fallback 算法如需调整，必须先引入 alias 表并完成历史兼容，不能在本项目中顺带改变。

### 6.2 原件指纹与解析修订

发现阶段使用廉价指纹判断是否需要任务：

```text
source_fingerprint = relative_path + size + mtime_unix_nano
```

完整解析时通过 `io.TeeReader` 同时计算原始 EML `SHA-256`，解析修订唯一键为：

```text
(message_key, raw_sha256, parser_version, policy_version)
```

- size/mtime 用于快速失效，不作为强内容完整性证明。
- `raw_sha256` 标识真实内容版本。
- `parser_version` 在 MIME、字符集或字段投影语义变化时递增。
- `policy_version` 在媒体类型/预览决策变化但 MIME 解析器不变时递增。
- 已存在同一修订时任务幂等成功，不重复写 Blob。

## 7. 持久化模型

首期使用 mail-node 本地 SQLite，开启 WAL、`busy_timeout` 和外键；数据库不通过网络共享，也不放在 NFS。SQLite 驱动选型必须保留当前 `CGO_ENABLED=0` 的 Linux 构建能力；如果实施决定引入 CGO，必须先单独更新构建、发布和交叉编译契约，不能作为依赖升级的隐式副作用。表名是设计契约，字段可在实施计划中补充审计列。

### 7.1 核心表

```text
messages
  message_key PK
  mailbox
  public_message_id
  maildir_unique_name
  relative_path
  source_size
  source_mtime_ns
  active_revision_id nullable
  state                 -- discovered/ready/partial/failed/stale/deleted
  received_at
  updated_at

parse_jobs
  job_id PK
  message_key
  source_fingerprint
  parser_version
  policy_version
  state                 -- queued/running/retry_wait/succeeded/dead
  priority
  attempts
  next_attempt_at
  lease_owner
  lease_expires_at
  last_error_code
  UNIQUE(message_key, source_fingerprint, parser_version, policy_version)

message_revisions
  revision_id PK
  message_key
  raw_sha256
  parser_version
  policy_version
  parse_status           -- ok/partial/failed/too_large
  subject/from/to/cc/date/received_at
  text_preview
  text_blob_key nullable
  html_blob_key nullable
  headers_blob_key nullable
  parse_error_code nullable
  created_at
  UNIQUE(message_key, raw_sha256, parser_version, policy_version)

message_parts
  revision_id
  part_index
  filename
  declared_content_type
  effective_content_type
  disposition
  content_id
  inline
  decoded_size
  blob_key nullable
  blob_sha256 nullable
  extraction_status      -- stored/metadata_only/denied/failed
  PRIMARY KEY(revision_id, part_index)
```

收件人数组等重复字段可以使用子表或 JSON；实现必须保证 SQLite 可按 `mailbox + received_at` 建立列表索引，不能依赖读取 JSON 完成分页。

### 7.2 小数据/大字节边界

MetadataStore 只保存有界元数据和最多 300 rune 的预览。以下内容进入 BlobStore：

- 完整 `text_body`；
- 完整 `html_body`；
- 完整 headers JSON；
- inline part 和普通附件的解码字节。

Blob key 由内容 SHA-256 派生，写入流程必须是“临时对象 -> 校验长度/hash -> 原子提交”。数据库只引用已提交对象；数据库提交失败产生的孤儿 Blob 由 reconciliation 清理。

为控制首期磁盘放大，可配置附件提取策略：

- `inline_and_body`：持久化正文与 inline，普通附件保留 `metadata_only`，下载时走兼容回退；用于迁移观察期。
- `all_parts`：正文、inline、普通附件全部持久化；达到“附件不再重解析 EML”的最终目标。

生产切到 `all_parts` 前必须按“原始 EML 写入量 x 保留天数 x 派生放大系数”重新核算磁盘。配置策略不能改变 part index 和 API 元数据。

## 8. 处理流程与同步边界

### 8.1 发现与入队

1. 发现器只处理已经离开 Maildir `tmp/` 的普通文件。
2. 对 `new/cur` 文件读取 stat 和必要邮件头，计算 `message_key` 与廉价指纹。
3. 在同一 SQLite 事务内 upsert `messages` 路径并 `INSERT OR IGNORE parse_jobs`。
4. `new -> cur` 只更新路径，不因 rename 创建新的解析修订。
5. 文件仍在写入、stat 前后变化或无法稳定打开时延迟重试，不解析半封邮件。

发现器可以沿用现有轮询，不要求引入文件系统 watcher。周期性 reconciliation 必须同时覆盖 `new` 和 `cur`，防止进程停机期间遗漏、手工移动或外部 IMAP 操作造成漂移。

### 8.2 解析协调器

ParseCoordinator 是唯一允许执行完整 MIME 解析的运行时入口：

1. 以 lease 领取任务，进程崩溃后 lease 到期可重领。
2. 使用全局有界 semaphore 控制完整解析并发；2C2G 默认并发应从 1 开始压测。
3. 对同一 `(message_key, fingerprint, versions)` 使用 singleflight，避免过滤、查询兜底和 worker 同时解析。
4. 打开文件后再次校验 stat；变化则结束旧任务并重新入队。
5. 一次解析同时生成查询模型、附件元数据、媒体证据和过滤特征。
6. 先提交 Blob，再在单个 SQLite 事务中写 revision、parts 并切换 `active_revision_id`。
7. 只有数据库事务成功后任务才标记 `succeeded`。

解析器不得在持有 SQLite 写事务时读取整个 EML 或上传大 Blob，避免长事务阻塞查询。

### 8.3 过滤与转发边界

过滤属于收信后的动作决策，不属于 SMTP 接收事务：

- legacy 过滤继续使用有界 header/body preview，不强制等待完整解析。
- dual shadow/dual filter 需要完整特征时，通过 ParseCoordinator 获取同一个 `ParseResult`，不得直接再次调用 `mailparse.ParseFile`。
- 过滤等待可以有独立超时；超时后的动作遵循现有过滤 fail-safe，不能把“读模型未完成”解释为邮件不存在。
- 转发 MIME header 修复如果需要解析证据，消费同一结果；原始 payload 重写规则仍以媒体安全设计为准。
- 过滤结果上报失败由现有 outbox 补偿，不回滚已经提交的解析修订。

### 8.4 查询边界

| 请求 | ready | pending/stale | failed/too_large |
|------|-------|---------------|------------------|
| 列表 | 读取 SQLite 索引和摘要 | 迁移期读取轻量 header 或旧链路兜底，并返回真实 parse status | 返回可用 header 元数据和明确状态 |
| 正文 | 读取活跃 revision 和正文 Blob | 迁移期通过 ParseCoordinator 合并解析并回填；超时沿用现有错误信封 | 不伪造空正文，返回明确解析错误 |
| inline/附件 | 读取 `message_parts` 后流式打开 Blob | Blob 未提取时仅在兼容策略允许下回退解析并回填 | 缺失/禁止预览返回对应 404/415/413 |
| raw EML | 直接读取 Maildir | 与解析状态无关 | 只要原件仍存在就可读取 |

每次读取派生数据前必须用已记录路径做一次廉价 `stat` 校验。路径因 `new -> cur` 失效时先通过路径索引/冷扫描更新；原件确实不存在时将消息标为 `stale/deleted`，不得继续把旧 Blob 当作当前邮件返回。

迁移期同步兜底是兼容机制，不是永久主路径。完成回填和稳定性验收后应通过指标逐步关闭列表全解析兜底；损坏邮件和运维恢复仍保留显式的“重新解析”命令。

## 9. BlobStore 边界

### 9.1 接口能力

```go
type BlobStore interface {
    Put(ctx context.Context, expectedSHA256 string, size int64, src io.Reader) (BlobRef, error)
    Stat(ctx context.Context, key string) (BlobInfo, error)
    Open(ctx context.Context, key string, byteRange *ByteRange) (io.ReadCloser, BlobInfo, error)
    Delete(ctx context.Context, key string) error
}
```

- 首期 `LocalBlobStore` 使用同文件系统临时文件 + atomic rename。
- `ManagedOSSBlobStore` 使用服务商原生 SDK 实现 multipart、checksum、Range、重试和生命周期。阿里云 OSS 等服务不能仅因提供部分 S3 兼容能力就直接复用 S3 实现；兼容范围必须以选定服务和 SDK 的集成测试为准。
- MinIO/S3 实现使用各自 SDK 的 multipart、checksum、Range 和服务端生命周期，但不改变上层 locator。
- mail-node 直接调用 BlobStore 完成上传和读取；收信后的对象写入不得先经过 mgmt-system。mgmt-system 继续负责鉴权、节点路由和现有下载响应代理，不承担对象持久化中转。
- handler 不能拼接本地绝对路径或对象 URL；只能使用 BlobStore 返回的 opaque key。
- 外部 API 默认继续走现有鉴权端点。预签名 URL 是后续显式能力，必须绑定短 TTL、允许的响应 disposition 和审计，不能把永久公开 URL 写进正文。

外置对象存储只消除本机 Blob 容量和 MinIO 常驻资源，不消除首次 MIME 解码成本。当前 `enmime` 仍会在 ParseCoordinator 中完整解析目标 EML；对象上传必须消费该次解析产物，不能为了上传再次解析原件。

### 9.2 引用与删除

Blob 以 SHA-256 去重时，删除不能直接“删文件”：

1. 删除/过期消息先在 MetadataStore 标记 revision 不可读并解除引用。
2. GC 只删除没有任何有效 revision 引用且超过 grace period 的 Blob。
3. GC 删除失败可重试，不影响邮件删除结果。
4. Maildir 恢复后若原 revision 和 Blob 仍在 grace period 内可重新关联，否则重新解析。
5. 对象生命周期规则不得早于数据库引用保留期，否则会制造大量悬空引用。

## 10. 状态、一致性与失败处理

### 10.1 状态机

```text
discovered -> queued -> running -> ready
                         |          |
                         |          +-> stale -> queued
                         +-> partial
                         +-> retry_wait -> running
                         +-> dead/failed

任意可读状态 -> deleted -> 派生数据解除引用 -> Blob GC
```

- `partial` 表示 MIME 有可用内容但存在解析警告，可以服务允许的字段。
- `failed` 表示当前修订不可提供结构化内容；原始 EML仍可读。
- `dead` 只用于任务达到重试上限，不删除原件。
- `stale` 表示源路径/指纹或解析版本变化，旧 revision 不可作为当前内容返回。

### 10.2 错误分类

| 类型 | 示例 | 策略 |
|------|------|------|
| 瞬时 | 文件暂时打不开、SQLite busy、Blob 网络超时 | 指数退避 + jitter，受最大次数和最大间隔限制 |
| 源变化 | stat 前后不一致、`new -> cur` | 不计业务失败，重新发现并入队 |
| 永久内容错误 | MIME 损坏、编码不可恢复、超过解析上限 | 记录稳定错误码，不无限重试；保留 raw |
| 容量 | Blob 磁盘水位、队列过长 | 停止派生 Blob 写入并告警，不删除 EML，不阻塞 SMTP 接收 |
| 程序版本 | parser panic、结果违反契约 | recover、任务失败、熔断对应 parser version，不能提交半成品 |

错误日志不得包含正文、附件字节、完整认证信息或未截断的攻击者可控 header。

### 10.3 崩溃一致性

- Blob 成功、数据库失败：形成孤儿 Blob，reconciliation 延迟删除。
- 数据库提交成功、进程在任务完成前崩溃：重领任务命中唯一修订后幂等完成。
- Maildir 删除、派生清理失败：消息先不可读，后台继续解除引用和 GC。
- SQLite 损坏或丢失：停止依赖读模型，切回受限旧读取路径并从 Maildir 重建；不得反向用 Blob 重建原件。
- BlobStore 丢失：对应 part 标记缺失并重新入队提取；原件存在时不算邮件丢失。

## 11. 资源与安全边界

### 11.1 资源限制

至少提供以下动态或启动配置，并设置保守默认值：

- `mime_parse.worker_concurrency`
- `mime_parse.max_message_bytes`
- `mime_parse.max_decoded_bytes`
- `mime_parse.max_part_bytes`
- `mime_parse.max_parts`
- `mime_parse.max_text_bytes` / `max_html_bytes`
- `mime_parse.job_max_attempts`
- `mime_parse.sync_fallback_timeout`
- `mime_blob.mode`、`mime_blob.provider`、`mime_blob.upload_concurrency`
- `mime_blob.max_disk_percent`、`mime_blob.gc_grace_period`

限制必须作用在解析和持久化入口，而不只是响应层。仅在 `enmime.ReadEnvelope` 返回后检查 part 大小，不能防止解码阶段的内存峰值；实施时必须用总 EML 上限、解析并发和进程 RSS 保护共同兜底。

队列积压不允许无限提高 worker 数。达到磁盘或 RSS 高水位时优先降并发/暂停 Blob 提取并告警，SMTP 原件投递与 raw EML 保留优先级最高。

4C4G mail-node 使用托管 OSS 时，完整解析 worker 和对象上传并发都应从 `1-2` 开始压测。上传必须流式并设置有界缓冲；不能因为对象存储位于外部就按 HTTP 请求数无界创建上传 goroutine。托管 OSS 降低的是本地存储服务开销，不是 `enmime` 首次解码的 CPU 和内存峰值。

### 11.2 内容安全

- 存储的 HTML 永远视为不可信内容，不在服务端“清洗后覆盖原文”。
- 浏览器展示继续使用 sandbox iframe、CSP、CID 白名单和远程资源阻断。
- Blob 响应必须经过附件/正文资源策略，统一设置 `nosniff` 和安全 disposition。
- SVG、HTML、脚本、可执行文件和强类型冲突不能因进入 BlobStore 而获得 inline 权限。
- 文件名、Content-ID、Content-Type 和解析错误必须清理控制字符并限制日志长度。
- LocalBlobStore 目录权限只允许 mail-node 账户访问；托管 OSS、S3 和 MinIO 凭据均按节点/桶最小授权并支持轮换，优先使用短期角色凭据而不是长期静态密钥。

## 12. 可观测性与容量验收

### 12.1 指标

至少暴露：

- `mime_parse_jobs{state}`、队列最老年龄、发现到 ready 延迟；
- parse duration、EML bytes、decoded bytes、part count；
- worker active、singleflight shared、fallback parse、parse error code；
- SQLite query/transaction duration、WAL size、busy 次数；
- Blob put/open/range duration、bytes、dedupe hit、missing、orphan count；
- 查询 read-model hit、stale、fallback、原件 stat failure；
- mail-node RSS、GC pause、CPU、Maildir/Blob 磁盘水位。

日志关联键使用 `node_uuid/message_key/job_id/revision_id`；外部 Message-ID 和文件名只在清理、截断后记录。

### 12.2 性能验收矩阵

必须在目标 Linux 机器使用真实分布压测：

| 维度 | 最低覆盖 |
|------|----------|
| 邮箱邮件数 | 10、1,000、10,000 |
| EML 大小 | 1 MiB、10 MiB、接近部署上限 |
| inline 数 | 0、10、50 |
| 并发 | 1、5、10、50 |
| 状态 | ready 命中、解析积压、节点重启、Blob 缺失、版本重建 |

验收必须记录 p50/p95/p99、首字节、吞吐、峰值 RSS、GC pause、CPU、磁盘 IOPS 和派生磁盘放大。至少验证：

1. ready 邮件打开正文和 10 张 inline 图片不会触发 EML 完整解析。
2. 同一新邮件的过滤和查询并发只执行一次相同版本解析。
3. worker 并发受限，50 个请求不会产生 50 份同一 EML 解码内存。
4. SQLite/Blob 不可用时原始投递不受影响，API 按规定回退或失败。
5. 删除后旧 Blob 不再可读，GC 和恢复符合 grace period。

## 13. 迁移与回滚

### P-1：正文契约前置验收

- 完成正文 MIME 投影实施计划 `S0/B1/B2/T3/F3/U3/R4`，修复真实问题 fixture。
- 冻结 `ParseResult`、`PartPath -> ExternalIndex`、warning code、parser version 和 policy version。
- 现有按需列表、正文、inline、附件、隔离区、过滤与转发已消费同一解析结果。
- 本阶段不得创建最终 SQLite 表、正文/part Blob 或历史回填任务。

门槛未满足时只能保留本设计文档和观测原型，不得进入下述 P0/P1。

### P0：基线与保护

- 为现有列表、详情、inline、附件链路增加解析次数、耗时、RSS 和 fallback 指标。
- 引入 ParseCoordinator、全局 semaphore 和 singleflight，但不改变 API 数据来源。
- 建立基准，确认每封邮件当前实际解析次数。

### P1：SQLite 任务与元数据影子写

- 引入本地 MetadataStore 和持久化任务。
- 解析结果影子写 revision/parts，查询仍走旧链路。
- 对列表字段、正文 hash、part index/类型/大小执行有界 shadow compare。
- 不做启动时全盘回填；按新邮件优先、近期历史邮件次优先的有界任务推进。

### P2：正文与 inline Blob 双读

- 启用 `inline_and_body`，查询优先读模型，未命中回退旧链路并回填。
- 按节点 canary，观察 read-model hit、fallback、差异、磁盘放大和解析延迟。
- inline endpoint 切换后验证一封多图邮件只读取各自 Blob。

### P3：全部 part 持久化

- 容量验收后启用 `all_parts`。
- 附件下载/预览优先 BlobStore，启用真正对象级 Range/ETag。
- 回填完成并达到稳定门槛后关闭普通查询的隐式全解析兜底，仅保留运维重建命令。

### P4：可选外置对象存储

- 托管 OSS/S3 以网络带宽、请求与流量费用、数据地域、凭据/KMS、生命周期和服务可用性作为准入条件；不套用自建 MinIO 的本地 CPU/内存基线。
- 自建 MinIO 必须满足容量文档中的独立节点、磁盘和网络条件，不能与 mail-node 混部。
- 为选定服务实现独立 BlobStore adapter。阿里云 OSS 使用原生 OSS adapter；S3 和 MinIO 使用各自经过验证的实现，不能把“API 大致兼容”作为完成条件。
- mail-node 直接写对象存储，新对象先双写 LocalBlobStore 和外置 BlobStore，并校验 size/checksum。
- 读取按“外置优先 -> Local 回退 -> Maildir 重建”逐节点 canary；历史对象后台限速回填，达到命中率和校验门槛后停止本地双写。
- Local Blob 保留完整回滚窗口后再由引用 GC 清理；切换配置不得立即删除本地对象。
- 预签名 URL、CDN 和跨地域能力单独评审，不能随 BlobStore 切换自动开放。

### 回滚原则

- 每阶段都有独立 feature flag：影子写、读模型优先、Blob 模式、附件 Blob 读取。
- 回滚只切回 Maildir 读取，不删除 SQLite、Blob 或原始 EML。
- parser/policy 版本不做原地覆盖；旧 revision 保留到 canary 和回滚窗口结束。
- 任何派生数据问题都不能要求从备份恢复 Maildir，除非原件自身已经丢失。

## 14. 验收标准

1. 正文 MIME 投影实施计划 `S0/B1/B2/T3/F3/U3/R4` 已完成，真实问题 fixture 和 MIME/媒体/安全矩阵通过。
2. 文档第 4 节的所有权边界在包依赖和接口中可被代码审查验证。
3. 正常新邮件只产生一个活跃解析修订；重复发现和进程重启保持幂等。
4. `new -> cur` 不改变内部物理身份、附件 index 或活跃解析内容。
5. 列表 ready 路径不调用 `enmime.ReadEnvelope`。
6. 正文、10 个 inline 和附件并发读取不重新解析 EML。
7. legacy/dual filter 与查询使用同一解析结果，特征和附件元数据无漂移。
8. Maildir、SQLite、Blob 任一单独故障都符合第 10 节降级规则，不产生“数据库有记录即认为原件存在”的错误。
9. parser/policy 升级支持影子重建、原子切换和版本回滚。
10. HTML、媒体、鉴权和响应头行为满足现有安全设计，不因持久化放宽。
11. 完成第 12 节目标 Linux 容量矩阵并记录可复现结果后，才能宣称生产容量或关闭旧链路兜底。

## 15. 实施前必须冻结的契约

进入编码前，实施计划必须把以下内容转成测试和迁移项：

1. `message_key`、Maildir unique name 规范化和重复 Message-ID 行为。
2. parser/policy version 递增规则和 revision 原子切换协议。
3. SQLite schema、迁移、备份排除/纳入策略与损坏恢复演练。
4. Blob key、checksum、临时对象、引用解除和 GC grace period。
5. BlobStore provider、厂商 SDK/兼容矩阵、mail-node 直连网络、凭据轮换和有界上传并发。
6. pending/partial/failed/too_large 在现有 HTTP 和 DataStream 中的兼容映射。
7. 同步 fallback 的超时、并发和最终关闭门槛。
8. 过滤等待 ParseCoordinator 时的 fail-safe，不得隐式改变现有投递动作。
9. 删除、隔离、释放、回收站恢复、保留期和手工 Maildir 变更的补偿流程。
10. backfill 优先级、速率、磁盘水位暂停和节点 canary/rollback 开关。
11. 基准 fixture、指标名称、告警阈值和生产验收报告模板。

以上契约未冻结前，不应先创建数据库表或引入任何外置对象存储；否则实现会把尚未决定的一致性语义固化到存储结构中。
