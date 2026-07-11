# MailHub UI 第二次优化改动设计文档

> 状态：O2-P1 至 O2-P4 已实现；O2-P5 仍为详细设计草案 | 最后校准：2026-07-11
> 范围：管理端 UI 二次打磨 + 邮件查询工作台布局修复 + 附件预览前后端 + 管理账号部署友好化
> 关联：`mailhub-ui-refresh-design.md`、`mailbox-creation-consolidation-design.md`、`attachment-download-design.md`、`t6-auth-design.md`

> **阅读说明：** 第 1 至第 5 节保留二次优化开始前的五项缺口描述和方案取舍。O2-P1（工作台布局）、O2-P2（创建邮箱 tab）、O2-P3（品牌色）和 O2-P4（附件安全预览）均已完成；当前仅 O2-P5 管理账号 Bootstrap 与恢复待实施。

---

## 1. 背景

Phase 4 已补齐浅色/深色主题、基础 token、移动端和状态样式，但仍有五个体验缺口：

1. 主题按钮目前只支持浅色/深色模式，不支持自定义品牌色。
2. 邮箱页创建表单常驻页面底部，信息结构显得拖沓，用户明确反馈“不好看”。
3. 邮件详情的附件只支持下载；图片、PDF、文本等常见附件缺少后台内预览。
4. 邮件查询页在查询某个邮箱后，邮件列表和邮件详情/查询区域存在挤压叠放风险，长邮箱地址、长主题、长 Message-ID 或窄屏宽度会破坏两列工作台布局。
5. 管理后台账号密码虽然已支持 `config.yaml` 的 `auth.admin_user/admin_pass`，但缺少显式首次初始化、DB hash 事实源、旧 session 失效、忘记密码恢复和后台改密入口。

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

### 2.4 邮件查询工作台布局

现有能力：

- `EmailsPage.jsx` 已形成「顶部查询表单 + 邮件列表 + 邮件详情」的审阅器结构。
- `.email-workbench` 使用两列 CSS Grid：左侧列表 `minmax(320px, 390px)`，右侧详情 `minmax(0, 1fr)`。
- 列表项对主题、发件人、摘要有一定截断；详情头部对主题和元信息已有 `overflow-wrap: anywhere`。

缺失：

- `.email-list-panel`、`.email-detail-panel`、`.email-list-item`、`.email-list-top` 等容器缺少系统性的 `min-width: 0`，子元素在长文本下会撑开 grid/flex 容器。
- 顶部查询表单与结果工作台的职责还不够分离，查询成功后表单、列表、详情在窄屏下容易形成视觉拥挤。
- 邮件列表列宽是固定区间，缺少中等屏宽下的断点策略；当可用宽度不足时，应主动切换为上下布局，而不是继续挤压两列。
- 列表项内附件标签、发件人、时间、主题之间没有完整的收缩优先级，长字段会挤占其他字段。

### 2.5 管理账号密码

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

- 首次部署缺少显式 `mgmt-server admin bootstrap` 初始化流程。
- 运行期登录仍直接依赖配置明文凭证，缺少 `admin_users` DB hash 事实源。
- 后台修改管理员账号密码入口。
- 改密或恢复后缺少基于 `credential_version` 的旧 session 失效策略。
- 忘记密码时缺少显式 `mgmt-server admin reset-password` 恢复命令。
- 部署文档中对管理账号初始化、迁移和恢复的明确说明。

---

## 3. 目标

1. 外观偏好可自定义，但不破坏运维后台的可读性和状态语义。
2. 邮箱页结构更清爽，创建操作成为明确菜单/tab，而不是底部大表单。
3. 邮件附件支持安全预览，减少反复下载再打开的操作成本。
4. 邮件查询页在查询邮箱、加载列表、打开详情时保持稳定的审阅器布局，不出现列表与详情/查询区域重叠。
5. 管理账号支持显式 bootstrap、DB hash 登录、后台改密和 CLI 恢复，避免运行期环境变量覆盖密码。

---

## 4. 需求清单

| 编号 | 需求 | 优先级 | 说明 |
|------|------|--------|------|
| O2-1 | 自定义主题品牌色 | P1 | 浅/深模式之外，支持色板和自定义 HEX |
| O2-2 | 创建邮箱 tab 化 | P0 | 排在「集成邮箱」后，顶部按钮切换到该 tab |
| O2-3 | 附件预览 | P0 | 图片/PDF/文本优先，其他类型降级下载 |
| O2-4 | 邮件查询工作台布局修复 | P0 | 查询邮箱后列表、详情和查询表单不叠放，长文本可控收缩 |
| O2-5 | 管理账号 Bootstrap 与恢复 | P0 | 首次初始化 + DB hash 事实源 + 后台改密 + session 失效 + CLI 恢复 |

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
| 常见位图 `image/*`（不含 SVG） | 弹窗内 `<img>` |
| `application/pdf` | 弹窗内 `<iframe>` |
| `text/plain` / `text/html` / `application/json` / `text/csv` | 后端按纯文本响应，前端 `<pre>` 展示 |

降级下载：

- Office 文档、压缩包、未知二进制。
- SVG 图片。
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
- HTML/XHTML/JSON/XML/JavaScript 等文本型附件统一以 `text/plain; charset=utf-8` 响应。
- 预览响应统一限制最大字节数，当前默认 10MB。
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

- HTML 附件按纯文本预览，不作为 HTML 执行。
- SVG 附件不进入图片预览器，统一降级下载。
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

## 8. 方案四：邮件查询工作台布局修复

### 8.1 布局目标

邮件查询页保持「查询条件区」和「结果审阅区」两个层级：

```text
页面标题 / 摘要指标
查询条件条（邮箱地址、分页大小、查询/重置）
结果审阅区：邮件列表 | 邮件详情
```

要求：

- 查询条件条始终独立成行，不参与左右两列工作台布局。
- 桌面宽屏保持左右两列：左侧列表用于扫描，右侧详情用于阅读。
- 中等屏宽或容器不足时主动切换为上下布局：列表在上、详情在下。
- 任何长邮箱、长主题、长 Message-ID、长附件名都只能在自己的容器内换行或截断，不允许撑开父级。

### 8.2 CSS 结构调整

`.email-workbench`：

- 保留 grid，但列定义改为更柔性的工作台宽度：

```css
grid-template-columns: minmax(280px, clamp(320px, 34vw, 420px)) minmax(0, 1fr);
```

- 增加 `min-width: 0`，确保右侧详情可收缩。
- 在 `max-width: 1180px` 或更保守断点下切换为单列：

```css
.email-workbench {
  grid-template-columns: 1fr;
}
```

`.email-list-panel`、`.email-detail-panel`：

- 增加 `min-width: 0`。
- 详情区最大宽度由父容器控制，不设会撑破布局的固定宽。
- 单列模式下移除不必要的最小高度或降低 `min-height`，避免详情空态占据过高屏幕。

`.email-list-item`：

- 增加 `min-width: 0`。
- 对内部 flex 行使用 `min-width: 0` + 明确的收缩规则。
- 主题行优先保留附件标签；主题文本用 ellipsis。
- 发件人与时间在桌面同排，窄屏下允许换行。

`.email-detail-head` / `.email-meta-grid`：

- 保留 `overflow-wrap: anywhere`。
- Message-ID、收件人列表、长邮箱地址使用可复制但不会撑宽的 code/文本样式。

### 8.3 查询态交互

查询成功后：

- 查询条仍停留在结果上方，便于更换 mailbox 或调整 page size。
- 邮件列表标题展示当前 mailbox，并对长邮箱做截断，完整值放 `title`。
- 结果为空、接口失败、加载中状态都占据列表列内部，不跨列覆盖详情。
- 若通过 URL 参数 `?mailbox=` 自动查询，页面加载后不滚动到详情区，避免用户误以为列表被遮挡。

选择邮件后：

- 桌面：右侧详情就地更新。
- 单列：详情位于列表下方；可以在列表项点击后滚动到详情标题，或提供轻量“返回列表”锚点。
- Message-ID 深链但没有列表时，详情区展示详情/错误态，左侧列表显示“由深链打开”的空态，不把空白列表压在详情上。

### 8.4 视觉约束

- 不新增嵌套卡片；查询条、列表面板、详情面板保持同级 section。
- 列表项高度允许随摘要两行增长，但 hover/active 不改变布局尺寸。
- 按钮文字和附件标签不得压缩到不可读；必要时标签换到下一行。
- 移动端不使用横向滚动承载主布局，只有正文 `<pre>` 和 HTML iframe 内部可独立滚动。

### 8.5 验收

- 输入普通邮箱查询后，列表和详情不重叠。
- 输入超长邮箱地址、超长主题、超长 Message-ID 后，页面宽度不被撑开。
- 1366px、1024px、768px、390px 宽度下均无横向页面滚动。
- 加载中、查询失败、空列表、已选中邮件、Message-ID 深链五种状态布局稳定。
- HTML 正文 iframe、Raw 元信息 `<pre>`、附件列表各自滚动或换行，不挤压列表列。

---

## 9. 方案五：管理账号 Bootstrap 与恢复

O2-P5 的详细方案以 `ui-second-optimization-p5-admin-bootstrap-design.md` 为准。本节只保留总设计口径。

### 9.1 运行期事实源

- 管理员密码不作为普通运行期配置。
- `mgmt-server serve` 不根据 env/config 创建、覆盖或重置管理员密码。
- 运行期登录只验证 DB 中 `admin_users.password_hash`。
- `config.yaml` 中的 `auth.admin_user/admin_pass` 仅作为旧部署迁移来源，不再作为长期登录事实源。

### 9.2 首次初始化

首次部署通过显式 CLI 完成管理员初始化：

```text
mgmt-server admin bootstrap --username <user> --password-file <path>
```

- bootstrap 只允许在未初始化状态执行。
- 成功后写入 `admin_users` 和 `system_state.admin_bootstrap=completed`。
- 重复 bootstrap 返回 already initialized，不覆盖既有管理员密码。

### 9.3 恢复与改密

- 忘记密码时使用 `mgmt-server admin reset-password` 显式恢复。
- 后台系统配置页新增“管理账号”模块，支持修改当前管理员密码。
- CLI 恢复和后台改密都递增 `credential_version`。
- session 中记录登录时的 `credential_version`；版本不一致时旧 session 失效。

### 9.4 环境变量策略

- 不提供 `MAILHUB_ADMIN_PASS` 这类运行期密码覆盖能力。
- 安装期如需环境变量，只允许被 `admin bootstrap` / `admin reset-password` CLI 显式读取。
- `MAILHUB_SESSION_COOKIE_SECURE` 可继续作为 session cookie 安全属性配置。

### 9.5 验收

- `mgmt-server admin bootstrap` 可完成首次管理员初始化。
- `mgmt-server serve` 不会根据 env/config 创建或重置管理员。
- DB hash 成为唯一运行期登录事实源。
- 后台改密和 `admin reset-password` 后旧 session 失效。
- 部署文档说明首次初始化、旧配置迁移和忘记密码恢复流程。

---

## 10. Phase 拆分与实施顺序

详细 phase 计划见：`ui-second-optimization-phase-plan.md`。

本次二次优化不一次性全改，按可独立验收、可独立发布的粒度拆分：

1. `O2-P0` 基线核对与验收样例：先固定长文本、窄屏、深链、空态/失败态等回归样例。
2. `O2-P1` 邮件查询工作台布局修复（`O2-4`）：优先解决当前最影响使用的列表/详情/查询区挤压叠放问题。
3. `O2-P2` 创建邮箱入口 tab 化（`O2-2`）：只调整邮箱页信息结构和创建入口。
4. `O2-P3` 主题品牌色偏好（`O2-1`）：纯前端增强，复用现有主题 token。
5. `O2-P4` 附件安全预览（`O2-3`）：前后端联动，新增 preview 端点，保持下载端点兼容。
6. `O2-P5` 管理账号 Bootstrap 与恢复（`O2-5`）：涉及安全、CLI、session 失效和部署说明，最后单独验证。

---

## 11. 测试计划

- 前端构建：`pnpm build`。
- 邮箱页：账号集合、回收站、集成邮箱、创建邮箱四个 tab 切换。
- 邮件查询页布局：
  - 普通邮箱查询后列表/详情不重叠。
  - 超长邮箱地址、超长主题、超长 Message-ID。
  - 加载中、查询失败、空列表、选中邮件、Message-ID 深链。
  - 桌面 1366px、窄桌面 1024px、平板 768px、移动 390px。
  - 页面主体无横向滚动；只有正文代码块/iframe 可在内部滚动。
- 主题：浅色/深色 + 4 个预设色 + 自定义 HEX + 重置。
- 附件预览：
  - PNG/JPEG。
  - PDF。
  - text/plain。
  - HTML/JSON 按纯文本展示。
  - SVG 降级。
  - zip 或 unknown 二进制降级。
  - 大于上限的附件降级。
- 认证：
  - `mgmt-server admin bootstrap` 首次初始化。
  - `mgmt-server serve` 不根据 env/config 覆盖管理员密码。
  - DB hash 登录。
  - 后台改密后旧 session 失效。
  - `mgmt-server admin reset-password` 恢复后旧密码不可登录。

---

## 12. 风险

| 风险 | 应对 |
|------|------|
| 自定义色导致对比度不足 | 对品牌色做基础亮度校验，不合格提示 |
| 邮件查询页断点切换影响桌面效率 | 只在容器不足或中等屏宽以下切单列，宽屏保留左右审阅器 |
| 长字段截断影响排障复制 | UI 截断但保留 `title` 或详情区完整可复制文本 |
| 预览 HTML 附件执行脚本 | HTML 附件按纯文本响应和展示 |
| SVG 图片执行脚本或外部资源 | SVG 不作为图片预览，降级下载 |
| 大附件预览占用内存 | 预览大小上限，下载走流式接口 |
| 运行期环境变量覆盖与后台改密互相冲突 | 不提供运行期管理员密码覆盖；安装和恢复必须走显式 CLI |
| 创建 tab 改动影响批量结果展示 | 保留 create view 状态，切换前不清空结果 |
