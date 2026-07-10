# MailHub UI 第二次优化 Phase 拆分计划

> 状态：实施计划草案 | 日期：2026-07-08  
> 来源：`ui-second-optimization-design.md`  
> 原则：每个 phase 独立开发、独立验收、可单独发布；不把 UI、附件、安全配置一次性混改。

---

## 1. 拆分原则

1. **先修正在影响使用的 P0 体验**：邮件查询工作台布局重叠/挤压优先级最高。
2. **纯前端先行，前后端联动后置**：先完成 CSS/React 范围内的低风险改动，再做附件预览和账号安全。
3. **同一 phase 只解决一类问题**：避免一次 PR 同时碰邮箱页、邮件页、认证和部署配置。
4. **每个 phase 都有回退路径**：前端 phase 可回退静态资源；后端 phase 需要接口兼容和配置开关。
5. **先不改变外部 API 合约**：附件预览新增端点，不改现有下载端点；管理员账号优化不破坏已有 `config.yaml` 登录方式。

---

## 2. Phase 总览

| Phase | 名称 | 覆盖需求 | 类型 | 建议优先级 | 是否可独立发布 |
|------|------|----------|------|------------|----------------|
| O2-P0 | 基线核对与验收样例 | 全部 | 文档/测试准备 | P0 | 已完成，见 `ui-second-optimization-p0-baseline.md` |
| O2-P1 | 邮件查询工作台布局修复 | O2-4 | 前端 CSS + 少量交互 | P0 | 已完成 |
| O2-P2 | 创建邮箱入口 tab 化 | O2-2 | 前端 React + CSS | P0 | 已完成 |
| O2-P3 | 主题品牌色偏好 | O2-1 | 前端 React + CSS token | P1 | 已完成 |
| O2-P4 | 附件安全预览 | O2-3 | 前后端联动 | P0 | 已完成，仍可补真实浏览器手点验收 |
| O2-P5 | 管理账号 Bootstrap 与恢复 | O2-5 | 后端认证 + CLI + 前端设置页 + 文档 | P0 | 待实现，建议最后做 |

---

## 3. O2-P0：基线核对与验收样例

### 目标

在动代码前固定当前问题样例，避免后续只凭肉眼判断“好像修了”。

### 范围

- 记录邮件查询页在以下宽度的当前表现：`1366px`、`1024px`、`768px`、`390px`。
- 准备长文本测试样例：
  - 超长邮箱地址。
  - 超长主题。
  - 超长 Message-ID。
  - 多附件且附件名很长。
- 明确构建命令和浏览器验收方式。

### 不做

- 不改 UI。
- 不改接口。
- 不处理附件预览和账号安全。

### 交付物

- 本地记录或截图。
- 后续 phase 共用的验收清单。

### 通过标准

- 能复现或确认邮件查询工作台的挤压/叠放风险。
- 每个后续 phase 都能使用同一组样例做回归。

---

## 4. O2-P1：邮件查询工作台布局修复

### 目标

先解决最明显的使用问题：查询邮箱后，查询条、邮件列表、邮件详情保持稳定，不被长文本或窄屏撑破。

### 范围

- 调整 `EmailsPage.jsx` 的必要结构和状态交互。
- 调整 `App.css` 中邮件工作台相关样式：
  - `.email-workbench`
  - `.email-list-panel`
  - `.email-detail-panel`
  - `.email-list-item`
  - `.email-list-top`
  - `.email-detail-head`
  - `.email-meta-grid`
- 增加系统性的 `min-width: 0`、断点和长文本收缩策略。
- 保持查询条件区独立成行，不参与列表/详情两列布局。

### 不做

- 不改附件预览。
- 不改邮箱创建入口。
- 不调整主题品牌色。
- 不改后端接口。

### 主要文件

- `mgmt-system/web/src/pages/EmailsPage.jsx`
- `mgmt-system/web/src/App.css`

### 验收

- 普通邮箱查询后列表和详情不重叠。
- 超长邮箱、超长主题、超长 Message-ID 不撑开页面。
- `1366px`、`1024px`、`768px`、`390px` 宽度下无页面级横向滚动。
- 加载中、查询失败、空列表、选中邮件、Message-ID 深链五种状态布局稳定。

### 回退

- 只需回退本 phase 的前端静态构建产物。
- 后端无变更。

---

## 5. O2-P2：创建邮箱入口 tab 化

### 目标

把创建邮箱从页面底部常驻表单调整为邮箱页内的明确 tab，让邮箱管理页首屏更清爽。

### 范围

- 邮箱页 tabs 改为：

```text
账号集合 | 回收站 | 集成邮箱 | 创建邮箱
```

- 顶部“创建邮箱”按钮改为切换到 `create` tab。
- 创建 tab 内保留现有能力：
  - 单个创建。
  - 批量创建。
  - 服务器/域名联动。
  - 批量结果展示。
  - CSV 账密下载。
- 创建成功后提供“查看账号集合”次操作。

### 不做

- 不改创建邮箱后端接口。
- 不改邮箱列表分页逻辑。
- 不改集成邮箱能力。

### 主要文件

- `mgmt-system/web/src/pages/MailboxesPage.jsx`
- `mgmt-system/web/src/App.css`

### 验收

- 邮箱管理页底部不再常驻创建大表单。
- 点击顶部“创建邮箱”直接进入创建 tab。
- 单个创建、批量创建和 CSV 下载仍可用。
- 账号集合、回收站、集成邮箱三个 tab 行为不回退。

### 回退

- 只需回退本 phase 的前端静态构建产物。
- 后端无变更。

---

## 6. O2-P3：主题品牌色偏好

### 目标

在已有浅色/深色模式基础上，增加品牌色选择和自定义 HEX，不改变成功、告警、危险等状态语义。

### 范围

- 主题按钮升级为外观设置 Popover。
- 支持：
  - 浅色 / 深色。
  - 预设品牌色。
  - 自定义 `#RRGGBB`。
  - 恢复默认。
- 使用 `localStorage` 持久化：

```text
localStorage.mailhub.theme
localStorage.mailhub.brandColor
```

- 前端派生：
  - `--color-brand`
  - `--color-brand-strong`
  - `--color-brand-soft`

### 不做

- 不写入 DB。
- 不做全局默认主题配置。
- 不让状态色跟随品牌色。

### 主要文件

- `mgmt-system/web/src/components/Layout.jsx`
- `mgmt-system/web/src/App.css`

### 验收

- 刷新页面后主题模式和品牌色保持。
- 浅色/深色下按钮、表格、表单、状态标签仍可读。
- 自定义品牌色不会污染删除、成功、告警等状态色。

### 回退

- 只需回退本 phase 的前端静态构建产物。
- 删除本地 `localStorage.mailhub.brandColor` 即可恢复默认观感。

---

## 7. O2-P4：附件安全预览

### 目标

为常见附件提供后台内预览，减少“下载到本地再打开”的操作成本，同时保留下载接口兼容性。

### 推荐拆成两个小步

#### O2-P4A：后端预览端点

- mgmt-system 新增管理端预览代理：

```http
GET /api/v1/admin/emails/:message_id/attachments/:index/preview?mailbox=<email>
```

- mail-node 新增内部预览端点：

```http
GET /internal/messages/:message_id/attachments/:index/preview?mailbox=<email>
```

- 支持 `image/*`、`application/pdf`、文本类。
- 设置安全响应头：
  - `Content-Disposition: inline`
  - `X-Content-Type-Options: nosniff`
- 不支持类型返回 `415`。
- 超出预览大小上限返回 `413`。

#### O2-P4B：前端预览 Modal

- 附件条目新增“预览”操作。
- 图片使用 `<img>`。
- PDF 使用 `<iframe>`。
- 文本使用 `<pre>` 并支持复制。
- 失败态提供原因和“下载附件”按钮。

### 不做

- 不改现有附件下载端点。
- 不把附件内容内联进邮件详情 JSON。
- 不向外部 API 开放 preview。
- 不支持 Office 文档在线预览。

### 主要文件

- `mail-node` 附件读取/内部接口相关文件。
- `mgmt-system` 管理端邮件附件代理接口相关文件。
- `mgmt-system/web/src/pages/EmailsPage.jsx`
- `mgmt-system/web/src/App.css`

### 验收

- 图片附件可在后台弹窗预览。
- PDF 附件可在后台 iframe 预览。
- 文本附件可预览并复制。
- zip、Office、未知二进制降级为下载。
- 大文件返回明确提示。
- 现有下载接口行为不变。

### 回退

- 前端可先隐藏预览按钮。
- 后端新增端点不影响现有下载接口。
- 若线上异常，回退 mgmt-system 和 mail-node 二进制到上一版本。

---

## 8. O2-P5：管理账号 Bootstrap 与恢复

### 目标

让管理员账号具备明确的安装期初始化、运行期 DB hash 事实源、后台改密、旧 session 失效和忘记密码恢复能力。

详细设计见 `ui-second-optimization-p5-admin-bootstrap-design.md`。本 phase 不采用“运行期环境变量覆盖管理员密码”的方案。

### 推荐拆成四个小步

#### O2-P5A：数据模型与 CLI bootstrap

- 新增 `admin_users` 和 `system_state`。
- 新增 `mgmt-server serve` / `admin bootstrap` 命令分发。
- `admin bootstrap` 支持 `--username`、`--password-file`，只在未初始化时创建管理员。
- 重复 bootstrap 不覆盖既有管理员密码。

#### O2-P5B：登录改造与 session 版本失效

- 登录改为验证 DB hash，不再使用 `cfg.Auth.AdminPass` 作为运行期事实源。
- session 写入 `admin_user_id`、`username`、`credential_version`。
- 管理端鉴权中校验当前用户状态和凭据版本。
- `credential_version` 变化后旧 session 自动失效。

#### O2-P5C：恢复 CLI 与后台账号设置

- 新增 `mgmt-server admin reset-password`。
- 系统配置页新增“管理账号”模块。
- 后台支持修改当前管理员密码。
- reset-password 和后台改密都递增 `credential_version`。

#### O2-P5D：文档、部署说明与收尾验证

- 更新 `config.example.yaml`，标注旧 `auth.admin_user/admin_pass` 仅用于迁移。
- 更新部署说明，记录首次 bootstrap、旧部署迁移、忘记密码恢复。
- 验证 P0-P4 不回退。

### 不做

- 不做多管理员。
- 不做权限角色系统。
- 不提供 `MAILHUB_ADMIN_PASS` 这类运行期密码覆盖能力。
- 不在 `mgmt-server serve` 启动时根据 env/config 创建、覆盖或重置管理员密码。

### 主要文件

- `mgmt-system/config.example.yaml`
- `mgmt-system/cmd/server/main.go`
- `mgmt-system/internal/store` 管理员与系统状态存储。
- `mgmt-system/internal/handler` 认证/账号设置相关文件。
- `mgmt-system/internal/middleware` session 校验相关文件。
- `mgmt-system/web/src/pages/ConfigPage.jsx` 或新增账号设置页。
- `docs/design/deployment-guide.md`、`README.md` 或部署说明相关文档。

### 验收

- `mgmt-server admin bootstrap` 可完成首次管理员初始化。
- 重复 bootstrap 不覆盖密码。
- `mgmt-server serve` 不根据 env/config 创建或重置管理员。
- 登录使用 DB hash；旧 config 明文密码不再作为运行期事实源。
- 后台修改密码后旧 session 失效。
- `mgmt-server admin reset-password` 可完成恢复，旧密码不可登录。
- 部署文档明确说明 bootstrap、迁移和 reset-password 流程。

### 回退

- 回退 mgmt-system 二进制到上一版本。
- 若已迁移到 DB 管理员，保留数据不删除。
- 后台改密入口可通过前端隐藏临时关闭。
- 若 P5 后端异常，先通过上一版本 `config.yaml` 登录链路恢复管理入口。

---

## 9. 建议执行顺序

1. **O2-P0 基线核对**：先固定验收样例。
2. **O2-P1 邮件查询工作台布局修复**：先解决当前最影响体验的问题。
3. **O2-P2 创建邮箱入口 tab 化**：继续处理 P0，但控制在邮箱页内。
4. **O2-P3 主题品牌色偏好**：纯前端增强，放在两个 P0 UI 稳定后。
5. **O2-P4 附件安全预览**：前后端联动，单独开发和验证。
6. **O2-P5 管理账号 Bootstrap 与恢复**：涉及安全、CLI、session 失效和部署说明，最后完整验证。

---

## 10. 每个 Phase 的完成定义

每个 phase 完成时必须满足：

1. 设计文档或 phase 记录已更新。
2. 本地构建通过。
3. 覆盖该 phase 的核心验收项。
4. 明确列出未覆盖项和下一 phase 接续点。
5. 若发布到国际机，记录备份路径、静态资源 hash 或二进制版本、线上烟测结果。

---

## 11. 当前推荐下一步

O2-P0 到 O2-P4 已完成，当前推荐进入 **O2-P5 管理账号 Bootstrap 与恢复**：

- 以 `ui-second-optimization-p5-admin-bootstrap-design.md` 作为实现事实源。
- 先做 O2-P5A 数据模型与 `admin bootstrap`，再改登录与 session 失效。
- O2-P4 附件安全预览可补一次真实浏览器手点验收，但不阻塞 O2-P5 开始。
