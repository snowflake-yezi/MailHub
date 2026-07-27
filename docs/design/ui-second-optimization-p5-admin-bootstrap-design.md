# MailHub O2-P5 管理账号 Bootstrap 与恢复设计

> 状态：已实现，待部署验收
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

### 11.1 登录页 UI 一体化

#### 现状问题

当前 `template/admin/login.html` 是独立的旧式居中卡片：使用绿色主色、`邮箱管理系统` 标题和自有 CSS 变量，未展示 MailHub 图标，也未复用 React 管理端的浅色/深色主题、品牌色偏好和控件状态。因此登录前后像两个不同产品。

P5 改造登录认证链路时，同步重做登录页。目标不是增加装饰，而是让用户在登录前就进入与管理端一致的品牌和视觉语境。

#### 页面结构

桌面端采用稳定的双区域布局，不做营销页式 Hero：

```text
┌──────────────────────────────────────────────────────────────┐
│ 品牌区（约 42%）              │ 登录工作区（约 58%）        │
│                               │                              │
│ [MailHub 图标] MailHub        │ 管理后台登录                 │
│ 邮件运维控制台                │ 使用管理员账号继续           │
│                               │                              │
│ 邮件、节点与策略统一管理      │ 用户名                       │
│                               │ [________________________]   │
│ 系统状态 · 账号安全 · 可观测  │ 密码                  [显示] │
│                               │ [________________________]   │
│                               │ [        登录控制台       ]  │
│                               │                              │
│                               │ 仅限授权管理员访问           │
└──────────────────────────────────────────────────────────────┘
```

- 品牌区使用实色 `--color-surface-alt`，以 MailHub 图标、产品名和一句定位建立识别；不使用渐变、装饰圆球、插画拼贴或功能宣传卡片。
- 登录工作区保持低噪声，表单容器最大宽度 `400px`，在区域中垂直居中；表单本身是唯一需要聚焦的面板，不再嵌套额外卡片。
- 第一视口必须看到完整产品名 `MailHub`，副标题统一为“邮件运维控制台”，与管理端侧栏一致。
- 桌面端整体最小高度使用 `100dvh`；两列使用稳定网格轨道，动态错误文案不得改变页面横向尺寸。

响应式规则：

- `>= 960px`：双区域布局，品牌区最小 `360px`，登录区承载表单。
- `600px–959px`：品牌区收窄为顶部横条，页面主体单列，表单宽度不超过 `420px`。
- `< 600px`：移除独立面板阴影和外边框，品牌头与表单直接落在页面上；水平内边距 `20px`，保证 `320px` 宽度可用。
- 移动端不得横向滚动；错误提示、长用户名和按钮文案必须在自身容器内换行或截断。

#### 视觉系统

登录页复用管理端现有 token 名称和语义：

| 类别 | 约束 |
|------|------|
| 品牌色 | `--color-brand`、`--color-brand-strong`、`--color-brand-soft` |
| 页面与面板 | `--color-bg`、`--color-surface`、`--color-surface-alt` |
| 文字与边框 | `--color-text`、`--color-muted`、`--color-border` |
| 状态 | 错误只使用 `--color-danger`，不得跟随品牌色 |
| 圆角 | 面板 `8px`，输入框/按钮 `6px`，与管理端一致 |
| 阴影 | 只用于桌面表单面板，使用现有 `--shadow`；移动端取消 |
| 字体 | 系统 UI 字体栈，与 `App.css` 一致；禁止随视口缩放字号 |

主题策略：

- 登录页在首帧读取 `mailhub.theme` 和 `mailhub.brandColor`，复用管理端已保存的外观偏好。
- 没有已保存主题时遵循 `prefers-color-scheme`；没有品牌色时使用 MailHub Blue `#2388ff`。
- 在 `<head>` 内用最小内联脚本提前设置 `data-theme` 和品牌色，避免页面加载后由浅色闪成深色。
- 登录页只提供太阳/月亮图标按钮切换浅深模式，并带 tooltip/`aria-label`；品牌色编辑仍留在登录后的主题面板，避免增加登录干扰。
- 登录页与 React SPA 共享 token 定义。实施时将公共 token 抽为静态 CSS，或由构建流程生成同源样式；不得继续维护两套手写色值。

#### 表单与交互

- 用户名输入使用 `autocomplete="username"`；密码输入使用 `autocomplete="current-password"`。
- 标签常驻输入框上方，不用 placeholder 代替 label。placeholder 仅提供输入示例且保持低对比度。
- 密码框右侧提供 `Eye` / `EyeOff` 图标按钮，点击只切换可见性，不改变输入框宽度；按钮必须有 `aria-label` 和 tooltip。
- 主按钮文案为“登录控制台”，使用品牌色；提交中显示固定尺寸 loading 图标并禁用重复提交，文案改为“正在登录”。
- Enter 提交保持原生表单行为。登录失败后保留用户名、清空密码，并将焦点移到密码框。
- 错误提示位于表单标题与第一个字段之间，使用 `role="alert"`，高度可增长但不覆盖字段。
- 不提供“忘记密码”网页入口。辅助文案使用“无法登录？请联系部署管理员通过恢复命令重置密码”，不得暴露具体用户名、账号状态或命令参数。
- `next` 继续作为隐藏字段提交；认证层必须保持当前的站内路径校验，UI 不展示跳转地址。

页面不显示系统健康、节点数量或版本接口数据。登录页是认证边界，不能为了视觉丰富而新增未鉴权信息泄露面。

#### 状态矩阵

| 状态 | 页面表现 |
|------|----------|
| 默认 | 用户名自动聚焦；字段、按钮和辅助文案完整 |
| 输入聚焦 | 使用品牌色边框和统一 focus ring，键盘焦点清晰 |
| 提交中 | 按钮禁用且保持原尺寸，显示 loading，不允许重复提交 |
| 凭据错误 | 通用错误提示；保留用户名、清空密码、焦点回密码 |
| 会话失效 | 可显示“登录状态已失效，请重新登录”，不透露失效原因 |
| 服务错误 | 显示“暂时无法登录，请稍后重试”，与凭据错误视觉同级 |
| 未初始化 | 不开放网页 bootstrap；显示“系统尚未初始化，请联系部署管理员” |
| 深色模式 | 背景、表单、输入、focus 和错误提示均满足可读性 |

#### 可访问性与安全约束

- 正文和输入文字对背景至少满足 WCAG AA；focus ring 不得仅依赖颜色差异。
- Logo 的 `alt` 使用空字符串，产品名由相邻文本表达，避免读屏重复。
- 登录错误使用统一口径，不区分用户不存在、密码错误、禁用或凭据版本变化。
- 不增加“记住密码”复选框；会话时长继续由服务端配置控制。
- 不在 HTML、JavaScript、注释或网络响应中输出 bootstrap secret、默认密码或 password hash。

#### 实施边界

第一版继续使用 Gin 服务端模板，不为单个登录页引入第二个 React 入口。模板只承担首屏和少量交互；图标优先复用现有 Lucide 产物或等价静态资源，不手绘新的 SVG 图标。

为避免后续再次割裂，公共品牌资产固定使用 `/admin/app/mailhub.png` 对应的同源静态文件，并将登录页样式从 HTML 内联 `<style>` 迁移到独立静态 CSS。P5B 完成时应删除旧绿色 `--brand: #1f4f46` 等孤立 token。

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

部署资产位于 `mgmt-system/Dockerfile` 与 `deploy/docker/compose.yaml`，操作步骤见 `docs/control-plane-deployment.md`。下方片段保留设计结构说明。

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

迁移兼容边界：旧部署密码可能不满足当前 release 12 位策略。`bootstrap-from-config` 仅在 `admin_bootstrap` 未完成时允许原样 bcrypt 迁移旧密码，并强制写入 `must_change_password=true`；普通 `bootstrap`、`reset-password` 和后台改密仍执行完整生产密码策略。

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
- 按 §11.1 重做登录页，复用 MailHub 品牌、主题 token 和响应式规则。
- 增加密码可见切换、提交中、未初始化、会话失效和服务错误状态。
- 旧 `auth.admin_user/admin_pass` 不再作为登录来源。

验收：

- DB 管理员可登录。
- 修改 DB 中 `credential_version` 后旧 session 失效。
- 禁用管理员后旧 session 失效。
- 登录前后品牌、主题、控件和错误状态视觉一致，桌面与移动端无溢出。

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
- 新版本可在隔离 Linux 环境完成 bootstrap、登录、改密和恢复演练。

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
- 未初始化、会话失效和服务错误返回登录页可识别的统一状态，不泄露账号细节。

CLI 测试：

- `admin bootstrap --password-file` 成功。
- 重复 bootstrap 返回 already initialized。
- `admin reset-password --password-file` 成功。
- 未初始化时 reset-password 返回错误。

前端验证：

- 登录页默认、提交中、凭据错误、会话失效、服务错误和未初始化状态完整。
- 登录页浅色/深色、已保存品牌色回显及 `320px` / `768px` / `1440px` 视口通过。
- 键盘 Tab 顺序、focus ring、读屏 label/alert 与密码可见按钮可用。
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
9. 部署指南包含升级、验证和回滚方法；具体生产记录存放在仓库外。
10. 登录页与 React 管理端共用 MailHub 品牌资产和视觉 token，浅色、深色及已保存品牌色一致。
11. 登录页在 `320px`、`768px`、`1440px` 下无重叠、横向滚动或动态状态引起的布局跳变。
