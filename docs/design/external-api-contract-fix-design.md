# 外部邮箱 API 文档补充与接口修复设计

> 状态：已完成（2026-07-10）。正式外部 API 文档、邮箱路径参数兼容和 scope 精确匹配均已落地；middleware、handler、mgmt-system 全量测试及 `go vet` 通过。

## 1. 背景与目标

项目已提供外部系统可调用的邮箱生成、邮箱查询、邮件列表、邮件正文和附件下载接口，但本次评估发现：

1. 外部 API 契约分散在 README、设计文档和代码中，缺少统一对接文档。
2. `RequireScope` 使用子串匹配判断权限，可能导致 scope 误放行。
3. 邮箱维度邮件列表路由参数名仍为 `:order_id`，与实际按邮箱地址查询的语义不一致。

本次目标是补齐正式外部 API 文档，并修复上述接口契约问题。

## 2. 范围

### 2.1 本次修改

- 新增 `docs/api/external-api.md` 作为外部系统正式对接文档。
- 修复 `mgmt-system/internal/middleware/auth.go` 的 scope 精确匹配逻辑。
- 修复 `mgmt-system/internal/handler/email.go` 的邮箱维度邮件列表路由参数名。
- 同步 README、架构概览、T6 鉴权设计、T8 MIME 设计中的接口描述。
- 增加 middleware 和 email 参数解析测试。

### 2.2 非目标

- 不修改数据库结构。
- 不引入新的 scope 名称，`disable` 暂继续使用 `mailbox:create`。
- 不改变外部 URL 形态。
- 不改变附件下载二进制流透传行为。

## 3. 设计决策

| 编号 | 决策 | 状态 | 说明 |
|------|------|------|------|
| D-1 | Scope 改为逗号分隔后的完整项匹配 | 已实现 | 避免 `email:readonly` 误匹配 `email:read` |
| D-2 | 保留 `*` 通配 scope | 已实现 | 兼容现有管理员级 token 配置 |
| D-3 | 邮箱维度邮件列表外部文档统一为 `{email}` | 已实现 | Gin 同一路径层级已有 `/mailboxes/:order_id`，不能再注册不同参数名的 `/mailboxes/:email/messages`，实现保留 `:order_id` 参数名并由 `mailboxParam` 兼容读取，外部契约仍描述为邮箱地址 |
| D-4 | `mailboxParam` 保留 `order_id` fallback | 已实现 | 降低旧测试/旧封装兼容风险 |
| D-5 | 附件下载继续例外于 JSON 信封 | 已实现 | 成功响应为二进制流，文档明确说明 |

## 4. 实现方案

### 4.1 Scope 精确匹配

在 `mgmt-system/internal/middleware/auth.go` 中新增 helper：

```go
func hasScope(scopes, required string) bool
```

逻辑：

1. `strings.Split(scopes, ",")`。
2. 对每项 `strings.TrimSpace`。
3. 空项跳过。
4. 任一项为 `*` 或与 required 完全相等则允许。
5. 否则拒绝。

### 4.2 路由参数名修复

在 `EmailHandler.RegisterRoutes` 中保留当前 Gin 路由注册：

```go
r.GET("/mailboxes/:order_id/messages", h.GetMailboxMessages)
```

原因是同一 Gin router 中已经存在 `GET /api/v1/mailboxes/:order_id`，如果在同一层级再注册 `GET /api/v1/mailboxes/:email/messages`，Gin 会因同层 wildcard 名称不同而 panic。外部契约仍以 `/api/v1/mailboxes/{email}/messages` 描述，因为该 path segment 的实际业务含义是邮箱地址。

`mailboxParam` 保持优先读取 `email`、其次 `order_id`、最后 query `mailbox`，从而既兼容现有 Gin 路由，也保留未来拆分路由组后的 `:email` 参数能力。

### 4.3 文档持久化

新增：

- `docs/api/external-api.md`：正式外部 API 对接文档。
- `docs/design/external-api-contract-fix-design.md`：本设计文档。

同步：

- `README.md`：文档索引与安全 API 简介。
- `docs/architecture-overview.md`：对外 API 表。
- `docs/design/t6-auth-design.md`：scope 映射与匹配规则。
- `docs/design/t8-mime-preprocessing-design.md`：邮箱维度主入口描述。

## 5. 测试方案

新增：

- `mgmt-system/internal/middleware/auth_test.go`
  - 覆盖 `*`、精确匹配、逗号分隔、空格 trim、子串误匹配拒绝、空 scope 拒绝。
  - 覆盖 `RequireScope` 缺 token、scope 不足、scope 满足三类行为。

- `mgmt-system/internal/handler/email_test.go`
  - 覆盖 `mailboxParam` 的 `email`、`order_id`、query fallback。
  - 覆盖带 `+`、`@` 的邮箱 path 参数解析。

验证命令：

```bash
cd mgmt-system
go test ./internal/middleware
go test ./internal/handler
go test ./...
```

如需确认数据面未受影响：

```bash
cd ../mail-node
go test ./...
```

## 6. 兼容性说明

- `/api/v1/mailboxes/{email}/messages` 的外部 URL 形态未变，客户端不需要调整；实现侧 Gin 参数名暂保留 `:order_id` 以避免与 `/api/v1/mailboxes/:order_id` 冲突。
- Scope 修复会收紧错误配置：过去依赖子串误匹配的 token 会开始返回 403，这是预期安全修复。
- 附件下载接口成功响应仍为二进制流，不套 JSON 信封。
