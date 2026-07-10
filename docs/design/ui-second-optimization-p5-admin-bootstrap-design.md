# MailHub O2-P5 管理账号 Bootstrap 与恢复设计

> 状态：详细设计草案  
> 日期：2026-07-10  
> 所属阶段：UI 第二次优化 `O2-P5`，作为本轮改造收尾工作  
> 关联文档：`ui-second-optimization-phase-plan.md`、`ui-second-optimization-design.md`、`t6-auth-design.md`、`deployment-guide.md`

---

## 1. 结论

`O2-P5` 不采用“环境变量长期覆盖管理员账号密码”的方案。

管理员密码不属于普通运行期配置，它属于安装期和恢复期产物。`mgmt-server serve` 主服务启动时永远不根据 env/config 创建、覆盖或重置管理员密码。

最终模型：

```text
安装期：mgmt-server admin bootstrap 显式创建初始管理员
运行期：DB admin_users 是管理员凭据唯一事实来源
日常改密：后台账号设置写入 DB hash，并提升 credential_version
恢复期：mgmt-server admin reset-password 显式重置密码
主服务：只负责认证和业务，不根据 env/config 改写凭据
```

这样可以避免重启、滚动发布、容器扩容或部署面板重写环境变量时产生反直觉的密码覆盖行为。

---

## 2. 背景

当前管理后台登录来自 `config.yaml`：

```yaml
auth:
  admin_user: "admin"
  admin_pass: "CHANGE-ME-ADMIN-PASSWORD"
```

现有实现中，`cmd/server/main.go` 将 `cfg.Auth.AdminUser` / `cfg.Auth.AdminPass` 直接注入 `AuthHandler`，`AuthHandler` 使用常量时间比较完成登录校验。

这带来几个问题：

1. 生产环境容易继续使用默认占位密码。
2. Docker、compose、面板部署虽然可以改配置文件，但没有清晰的首次初始化流程。
3. 明文管理员密码长期存在于配置文件中。
4. 后台改密没有事实来源，容易与 env/config 覆盖互相打架。
5. 缺少明确的“忘记管理员密码后如何恢复”的运维入口。

---

## 3. 设计目标

1. 生产环境不能使用默认占位管理员密码。
2. 首次管理员创建必须是明确的安装动作。
3. 主服务启动不做隐式管理员创建或密码重置。
4. 管理员密码只以 hash 形式存储在数据库。
5. 后台改密后所有旧 session 自动失效。
6. 忘记密码时走显式 CLI 恢复，而不是靠启动参数覆盖。
7. Docker Compose、Kubernetes、裸机部署都有清晰路径。
8. 作为 UI 第二次优化收尾工作，控制实现范围，不引入完整多用户权限系统。

---

## 4. 非目标

- 不做多管理员。
- 不做角色/权限系统。
- 不做 OAuth、OIDC、LDAP 等外部身份源。
- 不做邮件找回密码。
- 不在普通 `system_configs` 中保存管理员密码或 hash。
- 不提供 `MAILHUB_ADMIN_PASS` 这类运行期密码覆盖能力。
- 不让 `mgmt-server serve` 自动读取 env/config 修改管理员凭据。

---

## 5. 术语

| 术语 | 含义 |
|------|------|
| bootstrap | 首次初始化管理员账号的安装期动作 |
| recovery | 已初始化系统中，通过 CLI 显式恢复或重置管理员密码 |
| credential_version | 管理员凭据版本号，用于让旧 session 自动失效 |
| bootstrap state | 独立记录系统是否已经完成管理员初始化的状态 |
| serve | 正常启动管理后台服务的运行期命令 |

---

## 6. 目标状态机

第一版只实现轻量状态机：

```text
UNINITIALIZED
  未完成管理员初始化。
  允许执行 admin bootstrap。
  release 模式下 serve 拒绝启动。

INITIALIZED
  已完成管理员初始化。
  serve 正常启动。
  admin bootstrap 幂等返回 already initialized，不修改密码。

BROKEN
  bootstrap 状态显示已初始化，但没有 active 管理员或凭据损坏。
  serve 拒绝启动或输出明确 fatal。
  必须执行 admin reset-password 或人工修复 DB。
```

暂不单独实现 `LOCKED_OUT` / `INCONSISTENT` 复杂状态。后续如果引入多管理员和角色系统，再扩展状态机。

---

## 7. 数据模型

### 7.1 admin_users

第一版使用单表，避免过早引入完整 IAM 模型。

```sql
admin_users
- id
- username
- password_hash
- password_algo
- must_change_password
- credential_version
- status
- password_changed_at
- created_at
- updated_at
```

字段说明：

| 字段 | 说明 |
|------|------|
| `username` | 管理员登录名，第一版全局唯一 |
| `password_hash` | 密码 hash，不保存明文 |
| `password_algo` | 初始为 `bcrypt`，为未来迁移 Argon2id 预留 |
| `must_change_password` | 首次登录后是否必须修改密码 |
| `credential_version` | 每次改密或重置密码递增 |
| `status` | `active` / `disabled` |
| `password_changed_at` | 最近一次密码变更时间 |

第一版约束：

```text
只允许一个 active 管理员。
username 唯一。
password_hash 不允许为空。
credential_version 默认 1。
```

### 7.2 system_state

新增系统状态表，避免用“是否存在 admin 用户”推断系统是否初始化。

```sql
system_state
- key
- value
- updated_at
```

关键 key：

```text
admin_bootstrap = completed
```

为什么不能只看 `admin_users` 表：

- 管理员被误删后，系统不应该在下次启动时自动创建新管理员。
- 管理员被禁用后，系统不应该绕过既有安全状态。
- 凭据损坏属于恢复场景，不属于自动初始化场景。

---

## 8. CLI 设计

### 8.1 命令结构

新增命令形式：

```bash
mgmt-server serve
mgmt-server admin bootstrap
mgmt-server admin reset-password
```

兼容策略：

```text
mgmt-server
```

在第一版中继续等价于：

```bash
mgmt-server serve
```

避免现有 systemd、部署脚本一次性全部失效。

### 8.2 admin bootstrap

用于首次安装。

```bash
mgmt-server admin bootstrap \
  --config /etc/mgmt-system/config.yaml \
  --username admin \
  --password-file /run/secrets/mailhub_initial_admin_password \
  --must-change-password
```

参数：

| 参数 | 必填 | 说明 |
|------|------|------|
| `--config` | 否 | 配置文件路径，默认沿用 `CONFIG_PATH` 或 `config.yaml` |
| `--username` | 是 | 初始管理员用户名 |
| `--password-file` | 生产必填 | 从文件读取初始密码 |
| `--password` | 开发可用 | 直接传入密码，仅用于本地和兼容场景 |
| `--must-change-password` | 否 | 标记首次登录后必须改密 |

幂等规则：

```text
如果 admin_bootstrap != completed：
  校验密码策略
  创建 admin_users 记录
  写入 system_state admin_bootstrap=completed
  退出 0

如果 admin_bootstrap == completed：
  不修改密码
  不创建新管理员
  输出 already initialized
  退出 0
```

不提供 `--force` 覆盖 bootstrap。恢复场景必须使用 `admin reset-password`。

### 8.3 admin reset-password

用于已初始化系统的显式恢复。

```bash
mgmt-server admin reset-password \
  --config /etc/mgmt-system/config.yaml \
  --username admin \
  --password-file /run/secrets/mailhub_recovery_password \
  --must-change-password
```

规则：

```text
系统必须已初始化。
目标管理员必须存在。
新密码必须通过密码策略。
更新 password_hash。
credential_version + 1。
password_changed_at = now。
可选设置 must_change_password=true。
```

如果不传 `--password-file` / `--password`，后续可支持交互式隐藏输入。第一版在 Windows/CI 场景下可以先只支持显式参数和 password-file。

---

## 9. 环境变量与配置策略

### 9.1 不提供运行期管理员密码覆盖

不新增：

```text
MAILHUB_ADMIN_USER
MAILHUB_ADMIN_PASS
MAILHUB_ADMIN_FORCE_RESET
```

原因：

- 容器重启可能反复覆盖 DB 密码。
- 后台改密后，用户可能误以为新密码已生效，但 env 仍然覆盖。
- 滚动发布和扩容时，多实例可能争抢初始化或重置行为。
- 密码恢复应该是一次明确操作，而不是启动副作用。

### 9.2 安装期变量仅供 CLI 读取

如需适配部署面板，可提供 bootstrap 语义的变量，但只由 CLI 使用：

```text
MAILHUB_BOOTSTRAP_ADMIN_USERNAME
MAILHUB_BOOTSTRAP_ADMIN_PASSWORD_FILE
MAILHUB_BOOTSTRAP_ADMIN_PASSWORD
```

优先级：

```text
CLI flag > bootstrap env > 无值
```

`serve` 命令忽略这些变量。

### 9.3 session cookie secure

`MAILHUB_SESSION_COOKIE_SECURE` 可以保留，因为它是运行期 HTTP 安全配置，不是管理员凭据。

优先级：

```text
MAILHUB_SESSION_COOKIE_SECURE > DB system_configs session.cookie_secure > 默认值
```

后续可在 release 模式默认开启 `Secure`，但要考虑当前 Nginx TLS 终止和本地 HTTP 调试。

---

## 10. 密码策略

### 10.1 release 模式

首次 bootstrap 或 reset-password 时：

```text
密码不能为空。
密码长度至少 12 位。
拒绝常见弱密码：admin、password、123456、changeme、CHANGE-ME-ADMIN-PASSWORD。
拒绝与 username 相同。
```

### 10.2 非 release 模式

```text
允许较弱密码，但打印 warning。
如果未提供密码，可以后续支持自动生成随机密码。
```

第一版为了减少范围，不自动生成随机密码；开发环境仍要求明确传入密码。

---

## 11. 认证流程

新增 `AdminCredentialService`，集中处理管理员凭据：

```text
Verify(username, password) -> admin user + credential_version
ChangePassword(userID, currentPassword, newPassword)
ResetPassword(username, newPassword, mustChange)
Bootstrap(username, password, mustChange)
GetAccountInfo(userID)
```

`AuthHandler` 不再持有明文 `adminUser/adminPass`，改为调用服务：

```text
LoginAction:
  user = credentialService.Verify(username, password)
  sessionMgr.CreateSession(user.ID, user.Username, user.CredentialVersion)
```

登录失败继续返回通用错误：

```text
用户名或密码错误
```

不暴露用户是否存在、是否被禁用、是否必须改密等细节。

---

## 12. Session 失效策略

当前 session 是内存 map。第一版改造：

```go
type Session struct {
    AdminUserID        uint
    Username           string
    CredentialVersion  int
    CreatedAt          time.Time
    ExpiresAt          time.Time
}
```

每次管理端请求：

```text
1. 从 cookie 取 token。
2. 从 SessionManager 取 session。
3. 查询当前 admin user credential_version 和 status。
4. 如果 status != active，拒绝。
5. 如果 session.credential_version != user.credential_version，销毁当前 session，要求重新登录。
```

这样改密、CLI reset-password、后续迁移到 Redis/DB session 时，都能稳定让旧会话失效。

可选增强：

```text
SessionManager.DestroyAllForUser(adminUserID)
```

但它不是唯一安全手段。

---

## 13. 后台账号设置

### 13.1 API

新增管理端 API：

```http
GET /api/v1/admin/account
PUT /api/v1/admin/account
```

`GET` 返回：

```json
{
  "code": 0,
  "data": {
    "username": "admin",
    "must_change_password": false,
    "password_changed_at": "2026-07-10T12:00:00Z"
  }
}
```

`PUT` 请求：

```json
{
  "username": "admin",
  "current_password": "old password",
  "new_password": "new password"
}
```

规则：

- 修改密码必须提供 `current_password`。
- 修改用户名可以和修改密码一起提交。
- 新密码为空表示不改密码。
- 如果 `must_change_password=true`，登录后前端引导用户进入账号设置。
- 改密成功后 `credential_version + 1`，当前 session 也会因版本不一致而重新登录。

### 13.2 前端

在系统配置页新增“管理账号”模块，作为 P5 收尾入口。

展示：

- 当前用户名。
- 最近改密时间。
- 是否必须修改密码。

操作：

- 修改用户名。
- 修改密码。
- 保存后提示重新登录，并跳转 `/admin/login`。

视觉约束：

- 不把账号设置混入普通动态配置抽屉。
- 不展示 password hash。
- 不展示 bootstrap secret 或任何部署环境变量。

---

## 14. 部署形态

### 14.1 Docker Compose

```yaml
services:
  mailhub-bootstrap:
    image: mailhub:latest
    command:
      - mgmt-server
      - admin
      - bootstrap
      - --username
      - admin
      - --password-file
      - /run/secrets/mailhub_initial_admin_password
      - --must-change-password
    secrets:
      - mailhub_initial_admin_password
    depends_on:
      - db

  mailhub:
    image: mailhub:latest
    command: ["mgmt-server", "serve"]
    depends_on:
      mailhub-bootstrap:
        condition: service_completed_successfully

secrets:
  mailhub_initial_admin_password:
    file: ./secrets/mailhub_initial_admin_password
```

### 14.2 裸机/systemd

首次安装：

```bash
mgmt-server admin bootstrap \
  --config /etc/mgmt-system/config.yaml \
  --username admin \
  --password-file /etc/mgmt-system/secrets/initial-admin-password \
  --must-change-password
```

启动服务：

```bash
systemctl start mgmt-system
```

忘记密码：

```bash
systemctl stop mgmt-system
mgmt-server admin reset-password \
  --config /etc/mgmt-system/config.yaml \
  --username admin \
  --password-file /etc/mgmt-system/secrets/recovery-admin-password \
  --must-change-password
systemctl start mgmt-system
```

---

## 15. 兼容与迁移

### 15.1 现有部署兼容

当前线上已经依赖 `config.yaml auth.admin_user/admin_pass`。

迁移策略：

1. 首次运行新版本时，如果 `admin_bootstrap` 不存在且 `auth.admin_user/admin_pass` 存在：
   - 不由 `serve` 自动迁移。
   - 启动失败并提示执行 bootstrap 命令。
2. 为减少线上升级冲击，可以提供一次性迁移命令：

```bash
mgmt-server admin bootstrap-from-config \
  --config /etc/mgmt-system/config.yaml \
  --must-change-password
```

该命令只在未初始化时读取旧配置，创建 DB 管理员后完成 bootstrap。

### 15.2 config.example.yaml

`auth.admin_user/admin_pass` 标记为废弃：

```yaml
auth:
  # Deprecated: 管理员账号不再作为运行期配置。
  # 请使用 `mgmt-server admin bootstrap` 初始化管理员。
  admin_user: ""
  admin_pass: ""
```

第一版可以保留字段结构，避免破坏配置解析；但 `serve` 不再使用它们做登录校验。

---

## 16. 实施拆分

### O2-P5A：数据模型与 CLI bootstrap

范围：

- 新增 `admin_users` / `system_state` 模型。
- 新增 `AdminCredentialService`。
- 新增 `mgmt-server serve` / `admin bootstrap` 命令分发。
- `serve` 在 release 模式下检测未初始化并拒绝启动。
- `admin bootstrap` 支持 `--password-file`。

验收：

- 未初始化 release 模式 `serve` 启动失败并提示 bootstrap。
- `admin bootstrap` 创建管理员并写入 `admin_bootstrap=completed`。
- 重复 bootstrap 不覆盖密码。

### O2-P5B：登录改造与 session 版本失效

范围：

- `AuthHandler` 改为 DB 凭据校验。
- Session 加入 `admin_user_id` / `credential_version`。
- 管理端鉴权中校验当前用户状态和凭据版本。
- 旧 `auth.admin_user/admin_pass` 不再作为登录来源。

验收：

- DB 管理员可登录。
- 修改 DB 中 `credential_version` 后旧 session 失效。
- 禁用管理员后旧 session 失效。

### O2-P5C：恢复 CLI 与后台账号设置

范围：

- 新增 `admin reset-password`。
- 新增 `GET/PUT /api/v1/admin/account`。
- 系统配置页新增“管理账号”入口。
- 后台改密后跳转登录。

验收：

- CLI reset-password 后旧密码不可登录，新密码可登录。
- 后台改密后旧 session 失效。
- 修改用户名后新用户名生效。

### O2-P5D：文档、部署说明与收尾验证

范围：

- 更新 `deployment-guide.md`。
- 更新 `config.example.yaml` 注释。
- 更新 `t6-auth-design.md`，标记旧配置登录为历史方案。
- 记录线上升级步骤与回滚策略。

验收：

- Docker Compose、裸机部署文档完整。
- P0-P4 不回退。
- 新版本可在国际机完成 bootstrap、登录、改密、恢复演练。

---

## 17. 测试计划

后端单测：

- `Bootstrap` 首次创建成功。
- `Bootstrap` 重复执行不覆盖密码。
- 弱密码在 release 模式被拒绝。
- `Verify` 正确校验 bcrypt hash。
- `ChangePassword` 必须验证当前密码。
- `ResetPassword` 增加 `credential_version`。
- 未初始化状态检测正确。

Handler 测试：

- 登录成功创建带 `credential_version` 的 session。
- 错误密码返回 401。
- 改密后旧 session 请求 admin API 返回 401。
- `GET /api/v1/admin/account` 返回当前账号信息。
- `PUT /api/v1/admin/account` 改密成功。

CLI 测试：

- `admin bootstrap --password-file` 成功。
- 重复 bootstrap 返回 already initialized。
- `admin reset-password --password-file` 成功。
- 未初始化时 reset-password 返回错误。

前端验证：

- 系统配置页可进入管理账号模块。
- 密码确认、当前密码错误、保存中、保存成功、失败态完整。
- 保存成功后跳转登录页。

部署验证：

- 未 bootstrap 的 release 服务拒绝启动。
- bootstrap 后服务可启动。
- 登录后 P0-P4 功能仍可用。
- reset-password 后可用新密码登录。

---

## 18. 回滚策略

代码回滚：

- 若 P5 后端异常，回退 mgmt-system 二进制到上一版本。
- 旧版本仍读取 `config.yaml auth.admin_user/admin_pass`，因此回滚前确保旧配置仍保留。

数据回滚：

- 新增表 `admin_users` / `system_state` 不影响旧版本运行。
- 不删除旧配置字段。

前端回滚：

- 可隐藏“管理账号”模块。
- 其余配置模块不受影响。

---

## 19. 最终完成定义

O2-P5 完成时必须满足：

1. `mgmt-server serve` 不再使用 `config.yaml` 明文管理员密码登录。
2. `mgmt-server admin bootstrap` 可完成首次管理员初始化。
3. `mgmt-server admin reset-password` 可完成恢复。
4. 后台可修改管理员用户名和密码。
5. 密码只保存 hash。
6. 改密或 reset 后旧 session 失效。
7. release 模式未初始化不能启动。
8. 部署文档包含 Docker Compose 和裸机流程。
9. 已记录国际机升级、验证和回滚步骤。

