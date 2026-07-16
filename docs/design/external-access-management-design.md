# 外部访问管理设计

## 1. 背景

改造前外部 API 使用 `api_tokens.scopes` 逗号字符串鉴权。Token 由配置文件写入，管理端不可见，也无法完成应用命名、权限勾选、凭证轮换、过期控制和调用审计。Token 还以明文保存，无法满足开放平台长期扩展需要。

本设计将外部调用方建模为“外部应用”，由管理员在控制台创建应用、授予业务能力并签发凭证。调用方仍使用 Bearer Token，不需要管理后台登录。

## 2. 目标

- 管理端可创建、编辑、启用和停用外部应用。
- 权限以可读的业务功能展示，管理员通过勾选完成授权。
- 外部 API 路由在启动时自动同步到资源表，避免文档、鉴权和路由漂移。
- 一个应用可持有多个凭证，支持无中断轮换、独立撤销和到期时间。
- 完整 Token 只在签发时展示一次，数据库仅保存 SHA-256 哈希和可识别前缀。
- 记录应用级调用日志和最近使用信息。
- 将现有 `api_tokens` 一次性导入哈希凭证后删除明文表，升级后现有调用不中断。

## 3. 非目标

- 第一阶段不实现 OAuth2 授权码模式和用户委托授权。
- 第一阶段不引入角色/权限包。当前授权对象是外部应用，应用直接关联业务权限更容易理解；后续应用数量显著增长时可在其上增加角色层。
- 第一阶段不允许浏览器前端直接持有 Token，也不增加跨域开放策略。

## 4. 核心模型

### 4.1 `api_applications`

外部调用方主体。

| 字段 | 说明 |
|---|---|
| `id` | 主键 |
| `name` | 唯一名称，例如“出票中心” |
| `description` | 用途和负责人说明 |
| `enabled` | 应用总开关 |
| `created_at` / `updated_at` | 审计时间 |

### 4.2 `api_credentials`

应用凭证。一个应用可以有多个凭证。

| 字段 | 说明 |
|---|---|
| `application_id` | 所属应用 |
| `name` | 凭证名称，例如“生产主凭证” |
| `token_prefix` | 控制台可见的非敏感前缀 |
| `token_hash` | 完整 Token 的 SHA-256 哈希，唯一索引 |
| `enabled` | 凭证开关 |
| `expires_at` | 可选到期时间 |
| `last_used_at` / `last_used_ip` | 最近调用信息 |

Token 格式为 `mh_live_<base64url-random>`。完整值只在创建凭证的响应中返回一次，后续接口永不返回。

### 4.3 `api_permissions`

管理员可勾选的稳定业务能力。权限编码属于对外契约，不随 URL 调整而变化。

首批权限：

| 编码 | 功能组 | 名称 |
|---|---|---|
| `mailbox:create` | 邮箱账号 | 创建或复用邮箱 |
| `mailbox:read` | 邮箱账号 | 查询邮箱 |
| `mailbox:disable` | 邮箱账号 | 禁用邮箱 |
| `email:list` | 邮件读取 | 查询邮件列表 |
| `email:body` | 邮件读取 | 查看邮件正文 |
| `email:attachment` | 邮件读取 | 下载附件 |

### 4.4 `api_resources`

代码中已开放的 HTTP 接口清单，每个 method/path 一条记录，并关联一个权限编码。服务启动时以代码声明为事实源执行 upsert；代码中移除的资源标记为 `retired`，不物理删除。

自动同步只登记接口，不自动给任何应用增加新的权限编码。新增权限因此默认不可访问。

资源路径限制为 175 字符，使 `method + path` 复合唯一索引在生产 MariaDB 5.5 的 utf8mb4 767 字节索引上限内可创建；当前外部路由长度均远低于该限制。

### 4.5 `api_application_permissions`

应用与权限的多对多关系，以 `(application_id, permission_code)` 建立唯一约束。鉴权只做完整权限编码匹配，不做前缀或子串匹配。

### 4.6 `api_access_logs`

记录应用、凭证、权限编码、method、path、HTTP 状态、来源 IP、耗时和调用时间。日志写入失败不得影响业务响应。

## 5. 路由注册

外部路由通过统一声明注册：

```go
registry.Register(api, APIRoute{
    Method:         http.MethodPost,
    Path:           "/mailboxes",
    PermissionCode: "mailbox:create",
    Group:          "邮箱账号",
    Name:           "创建或复用邮箱",
    Handler:        mailboxH.CreateMailbox,
})
```

注册动作同时完成：

1. 向 Gin 注册路由。
2. 挂载权限校验中间件。
3. 收集资源元数据，并在启动阶段同步到数据库。

任何外部接口都不得绕开统一注册器直接挂到 `/api/v1`。

## 6. 鉴权流程

```text
Bearer Token
  -> SHA-256 查找 api_credentials
  -> 检查凭证 enabled / expires_at
  -> 检查所属应用 enabled
  -> 加载应用权限
  -> 精确匹配接口 permission_code
  -> 执行业务处理器并写访问日志
```

升级启动时会将现有 `api_tokens` 及配置中的旧 Token 转换为管理端可见的外部应用和哈希凭证。系统逐项确认哈希凭证存在后删除明文表，运行期不再提供旧表回退。旧 scope 仅在导入阶段映射：

- `mailbox:create` 映射到 `mailbox:create` 和 `mailbox:disable`。
- `mailbox:read` 映射到 `mailbox:read`。
- `email:read` 映射到 `email:list`、`email:body` 和 `email:attachment`。
- `*` 映射到所有权限。

迁移后的应用可在管理端继续编辑权限和签发新凭证。旧表删除后，配置中的 Token 只能用于确认同一凭证已完成导入，不能创建新凭证；管理员确认升级完成后应删除这些明文配置。

## 7. 管理端交互

新增“外部访问”页面：

- 列表显示名称、状态、授权功能、有效凭证数、最近调用和创建时间。
- 新建抽屉填写名称、说明并按功能组勾选权限。
- 创建成功后弹出一次性凭证对话框，支持复制；关闭后无法再次查看完整值。
- 详情抽屉可修改授权和状态、签发新凭证、永久删除凭证、查看最近调用。
- 权限变更即时生效，不需要重启服务。

停用应用会使其全部凭证立即失效；撤销单个凭证不影响同应用的其他凭证。

## 8. 管理 API

所有管理接口位于 `/api/v1/admin`，使用现有管理员 Session：

- `GET /external-applications`
- `POST /external-applications`
- `PUT /external-applications/:id`
- `POST /external-applications/:id/credentials`
- `DELETE /external-applications/:id/credentials/:credential_id`
- `POST /external-applications/:id/credentials/:credential_id/revoke`
- `GET /external-applications/:id/logs`
- `GET /api-permissions`

管理端页面使用 `DELETE` 永久删除凭证，并在提交前二次确认。`revoke` 接口仅保留兼容；它只将凭证设为不可用，不包含定时清理逻辑。

## 9. 安全约束

- 只允许 HTTPS 对外开放 API。
- Token 使用至少 32 字节密码学随机数。
- 数据库、日志和错误响应不得包含完整 Token。
- 权限和应用状态每次请求从数据库读取，保证撤销即时生效。
- 新权限和新资源默认不授权。
- 管理操作继续受管理员 Session 和 CSRF/SameSite 部署策略保护。

## 10. 验收标准

1. 管理员可创建命名应用并勾选权限，首次获得一次性 Token。
2. Token 只能访问已授权功能，未授权功能返回 HTTP 403 / code 1005。
3. 停用应用、撤销凭证或凭证到期后返回 HTTP 401 / code 1004。
4. 数据库中不存在新 Token 明文。
5. 同一应用可同时使用两个有效凭证，并可单独撤销其中一个。
6. 外部路由均出现在资源表，method/path/权限编码与代码一致。
7. 旧配置 Token 在兼容期内行为不变。
8. 管理端可查看凭证前缀、最近使用时间和调用日志。
