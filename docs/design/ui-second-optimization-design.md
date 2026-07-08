# MailHub UI 第二次优化改动设计文档

> 状态：设计草案 | 日期：2026-07-08
> 范围：管理端 UI 二次打磨 + 附件预览前后端 + 管理账号部署友好化
> 关联：`mailhub-ui-refresh-design.md`、`mailbox-creation-consolidation-design.md`、`attachment-download-design.md`、`t6-auth-design.md`

---

## 1. 背景

Phase 4 已补齐浅色/深色主题、基础 token、移动端和状态样式，但仍有四个体验缺口：

1. 主题按钮目前只支持浅色/深色模式，不支持自定义品牌色。
2. 邮箱页创建表单常驻页面底部，信息结构显得拖沓，用户明确反馈“不好看”。
3. 邮件详情的附件只支持下载；图片、PDF、文本等常见附件缺少后台内预览。
4. 管理后台账号密码虽然已支持 `config.yaml` 的 `auth.admin_user/admin_pass`，但 Docker/环境变量覆盖、首次部署提醒、后台修改入口还不完整。

---

## 2. 现状核对

### 2.1 自定义主题颜色

现有 `mailhub-ui-refresh-design.md` 只要求：

- 浅色优先。
- 预留/增加深色模式。
- 固定色彩 token。

缺失：

- 用户选择品牌色。
- 预设色板。
- 自定义颜色持久化。
- 恢复默认配色。

### 2.2 创建邮箱入口

现有 `mailbox-creation-consolidation-design.md` v0.1 明确要求“创建区域始终位于页面最后”。实际 UI 已按该方向实现，但体验不佳。

第二次优化改为：

- 创建邮箱作为邮箱页 tab。
- tab 顺序：账号集合、回收站、集成邮箱、创建邮箱。
- 页面顶部“创建邮箱”按钮切换到创建 tab。

### 2.3 附件预览

现有能力：

- mail-node 已能解析附件元数据。
- mgmt 已能代理二进制附件下载。
- HTML 正文中的 inline 图片可通过同源附件端点展示。

缺失：

- 普通附件的“预览”按钮和预览器。
- 用于预览的安全响应策略。
- 大文件、未知类型、Office 文件等不可预览场景的降级。

### 2.4 管理账号密码

现有能力：

- `mgmt-system/config.example.yaml` 已有：

```yaml
auth:
  admin_user: "admin"
  admin_pass: "CHANGE-ME-ADMIN-PASSWORD"
```

- `main.go` 已将 `cfg.Auth.AdminUser` / `cfg.Auth.AdminPass` 注入 `AuthHandler`。
- `t6-auth-design.md` 已记录配置文件凭证。

缺失：

- 环境变量覆盖，方便 Docker / compose / 面板部署。
- 默认密码安全告警或 fail-fast 策略。
- 后台修改管理员账号密码入口。
- 部署文档中对管理账号的明确说明。

---

## 3. 目标

1. 外观偏好可自定义，但不破坏运维后台的可读性和状态语义。
2. 邮箱页结构更清爽，创建操作成为明确菜单/tab，而不是底部大表单。
3. 邮件附件支持安全预览，减少反复下载再打开的操作成本。
4. 管理账号支持配置文件、环境变量和后台修改，适配 Docker 部署。

---

## 4. 需求清单

| 编号 | 需求 | 优先级 | 说明 |
|------|------|--------|------|
| O2-1 | 自定义主题品牌色 | P1 | 浅/深模式之外，支持色板和自定义 HEX |
| O2-2 | 创建邮箱 tab 化 | P0 | 排在「集成邮箱」后，顶部按钮切换到该 tab |
| O2-3 | 附件预览 | P0 | 图片/PDF/文本优先，其他类型降级下载 |
| O2-4 | 管理账号部署友好化 | P0 | 环境变量覆盖 + 默认密码警告/阻断 + 后台改密 |

---

## 5. 方案一：主题颜色偏好

### 5.1 UI

主题按钮从单一 icon button 升级为外观 Popover：

- 模式：浅色 / 深色。
- 品牌色预设：MailHub Blue、Mint、Cyan、Coral。
- 自定义颜色：`input[type=color]` + `#RRGGBB` 文本输入。
- 重置默认。

### 5.2 持久化

第一阶段使用浏览器本地偏好：

```text
localStorage.mailhub.theme = "light" | "dark"
localStorage.mailhub.brandColor = "#2388ff"
```

第二阶段如需全局默认，再写入 `system_configs`：

```text
ui.theme_mode_default
ui.brand_color_default
```

### 5.3 颜色派生

前端只保存主品牌色：

- `--color-brand` 使用用户选择值。
- `--color-brand-strong` 由主色加深或提高饱和度派生。
- `--color-brand-soft` 由主色加透明或混合 surface 派生。

状态色 `success/warning/danger/info` 默认不跟随品牌色，避免“删除按钮变品牌色”这类语义污染。

### 5.4 验收

- 刷新页面后主题模式和品牌色保持。
- 浅色/深色都可读。
- 自定义品牌色不会改变危险/成功/告警状态语义。

---

## 6. 方案二：创建邮箱入口调整

### 6.1 信息结构

邮箱页 tabs 改为：

```text
账号集合 | 回收站 | 集成邮箱 | 创建邮箱
```

顶部“创建邮箱”按钮：

- 当前行为：滚动到底部 `#mailbox-create-panel`。
- 新行为：`setView('create')`，并将页面滚动到顶部或当前 tab 内容区。

### 6.2 创建 tab 内容

创建 tab 保留现有能力：

- 邮箱服务器选择。
- 域名选择。
- 单个创建。
- 批量创建。
- 批量结果和账密 CSV 下载。

需要调整：

- `view === 'create'` 时展示创建 section。
- 移除页面底部常驻创建 section。
- `load()` 不应因切换到 create tab 意外清空 batch result。
- 创建成功后提供“查看账号集合”次按钮。

### 6.3 验收

- 邮箱管理页首屏不再被底部创建表单拖长。
- 点击顶部“创建邮箱”直接进入创建 tab。
- 创建结果在创建 tab 内清晰展示。
- 账号集合、回收站、集成邮箱不受影响。

---

## 7. 方案三：附件预览

### 7.1 支持范围

第一阶段支持：

| 类型 | 预览方式 |
|------|----------|
| `image/*` | 弹窗内 `<img>` |
| `application/pdf` | 弹窗内 `<iframe>` |
| `text/plain` / `text/html` / `application/json` / `text/csv` | 后端读取文本或 iframe/preview endpoint |

降级下载：

- Office 文档、压缩包、未知二进制。
- 超过预览大小上限的附件。
- MIME 类型不可信或内容嗅探失败。

### 7.2 后端接口

保留现有下载端点：

```http
GET /api/v1/admin/emails/:message_id/attachments/:index?mailbox=<email>
```

新增管理端预览端点：

```http
GET /api/v1/admin/emails/:message_id/attachments/:index/preview?mailbox=<email>
```

mail-node 内部端点同步新增：

```http
GET /internal/messages/:message_id/attachments/:index/preview?mailbox=<email>
```

行为：

- 对允许预览的类型返回 `Content-Disposition: inline`。
- 设置 `X-Content-Type-Options: nosniff`。
- 对文本类限制最大字节数，例如默认 1MB。
- 对不可预览类型返回 `415` JSON：`unsupported preview type`。
- 对超限返回 `413` JSON：`attachment too large to preview`。

### 7.3 前端交互

附件条目新增：

- 预览按钮：仅对可预览类型显示，或始终显示但不可预览时 toast 提示。
- 下载按钮：保留。

预览 Modal：

- 标题显示文件名、类型、大小。
- 图片自适应容器，不裁切。
- PDF 使用 iframe。
- 文本使用 `<pre>`，支持复制。
- 失败态显示原因和“下载附件”按钮。

### 7.4 安全边界

- HTML 附件默认不执行脚本；如 iframe 预览，必须使用 `sandbox`。
- 不自动加载外部资源。
- 不将附件内容内联进邮件详情 JSON。
- 预览端点只用于 admin session；外部 API 暂不开放 preview，避免扩大暴露面。

### 7.5 验收

- 图片附件可在后台弹窗中预览。
- PDF 附件可在后台 iframe 中预览。
- 文本附件可预览并复制。
- 不支持类型仍可下载，且提示明确。
- 下载接口兼容不变。

---

## 8. 方案四：管理账号部署友好化

### 8.1 配置优先级

建议优先级：

```text
环境变量 > config.yaml > 默认值/空值
```

新增环境变量：

```text
MAILHUB_ADMIN_USER
MAILHUB_ADMIN_PASS
MAILHUB_SESSION_COOKIE_SECURE
```

Docker / compose 部署可直接注入：

```yaml
environment:
  MAILHUB_ADMIN_USER: "admin"
  MAILHUB_ADMIN_PASS: "${MAILHUB_ADMIN_PASS}"
```

### 8.2 默认密码策略

生产模式 `server.mode=release`：

- `admin_pass` 为空：启动失败。
- `admin_pass` 等于 `CHANGE-ME-ADMIN-PASSWORD` 或 `change-me-admin-password`：启动失败。

非 release：

- 允许启动，但日志明确告警。

### 8.3 后台账号设置

系统配置页新增“管理账号”模块，或新增 `/settings/account`：

- 当前管理员用户名展示。
- 修改用户名。
- 修改密码：当前密码、新密码、确认密码。
- 修改成功后清理当前 session，要求重新登录。

### 8.4 存储决策

第一阶段：

- 启动凭证仍来自配置/环境变量。
- 后台修改写入 DB `system_configs`，优先级高于 config.yaml 但低于显式环境变量。

第二阶段可选：

- 独立 `admin_users` 表，支持多管理员、密码 hash、审计日志。

### 8.5 密码安全

- DB 中不保存明文管理密码，保存 bcrypt hash。
- config/env 明文密码只作为首次启动或覆盖来源。
- 登录时兼容：
  - 若 DB hash 存在，优先验证 DB hash。
  - 若显式环境变量存在，优先环境变量。

### 8.6 验收

- Docker 环境变量可覆盖配置文件账号密码。
- release 模式不能使用默认占位密码启动。
- 后台可修改密码，修改后旧 session 失效。
- 部署文档明确说明如何设置 `MAILHUB_ADMIN_PASS`。

---

## 9. 实施顺序

1. `O2-2` 创建邮箱 tab 化：纯前端，收益最大，风险低。
2. `O2-1` 自定义主题品牌色：前端局部改动，复用现有 token。
3. `O2-3` 附件预览：前后端联动，需测试 MIME、安全和大文件。
4. `O2-4` 管理账号部署友好化：涉及安全和部署，最后做完整验证。

---

## 10. 测试计划

- 前端构建：`pnpm build`。
- 邮箱页：账号集合、回收站、集成邮箱、创建邮箱四个 tab 切换。
- 主题：浅色/深色 + 4 个预设色 + 自定义 HEX + 重置。
- 附件预览：
  - PNG/JPEG。
  - PDF。
  - text/plain。
  - zip 或 unknown 二进制降级。
  - 大于上限的附件降级。
- 认证：
  - config.yaml 登录。
  - 环境变量覆盖登录。
  - release 默认密码阻断。
  - 后台改密后旧 session 失效。

---

## 11. 风险

| 风险 | 应对 |
|------|------|
| 自定义色导致对比度不足 | 对品牌色做基础亮度校验，不合格提示 |
| 预览 HTML 附件执行脚本 | iframe sandbox，默认不开放脚本 |
| 大附件预览占用内存 | 预览大小上限，下载走流式接口 |
| 环境变量与后台 DB 凭证冲突 | 明确优先级并在 UI 中提示“环境变量覆盖中” |
| 创建 tab 改动影响批量结果展示 | 保留 create view 状态，切换前不清空结果 |
