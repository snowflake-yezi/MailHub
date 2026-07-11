# MailHub UI 第二次优化 O2-P0 基线记录

> 状态：完成（历史基线；O2-P1 至 O2-P4 已完成，当前下一项为 O2-P5） | 日期：2026-07-08
> 所属计划：`ui-second-optimization-phase-plan.md`  
> 目标：在进入 O2-P1 前固定邮件查询工作台的验收样例、风险证据和构建基线。

---

## 1. 本阶段结论

O2-P0 不修改业务实现，只固定后续修复的判断口径。

当前邮件查询页已经具备「查询条件区 + 邮件列表 + 邮件详情」结构，但 CSS 收缩链不完整。后续 O2-P1 应优先补齐工作台、左右 panel、列表项和附件/正文区域的防溢出策略，并用本文样例做回归。

---

## 2. 构建基线

在 `mgmt-system/web` 执行：

```bash
pnpm run build
```

结果：通过。

当前构建产物：

```text
../template/static/admin-app/index.html
../template/static/admin-app/assets/index-D9fVxBuO.css
../template/static/admin-app/assets/index-C6DJ3KYS.js
```

说明：本阶段只记录基线，不以构建产物 hash 变化作为功能完成依据。

---

## 3. 代码证据

### 3.1 页面结构

`EmailsPage.jsx` 当前结构是：

```text
page-header
summary-grid
section.email-search-panel
div.email-workbench
  section.email-list-panel
  section.email-detail-panel
```

这符合「查询条件区」与「结果审阅区」分层的目标，O2-P1 不需要大幅重构页面层级。

### 3.2 当前风险点

`App.css` 当前邮件工作台相关样式存在以下风险：

- `.email-workbench` 使用 `grid-template-columns: minmax(320px, 390px) minmax(0, 1fr)`，但自身未设置 `min-width: 0`。
- `.email-list-panel`、`.email-detail-panel` 当前只有 `min-height: 560px`，未设置 `min-width: 0`。
- `.email-list-item` 当前未设置 `min-width: 0`，内部长内容依赖子元素局部截断。
- `.email-list-top`、`.email-list-meta` 是 flex 行，虽然部分子元素有截断，但父级缺少完整收缩边界。
- `.email-search-form` 在 `760px` 以下才切单列，中等宽度下需要继续观察查询条和工作台的组合高度/密度。
- `.attachment-item` 只在 `760px` 以下改为纵向，长附件名在桌面/中等宽度下仍需要 P1 验证。

### 3.3 已有保护

当前也已有一些可保留的保护：

- `.search-input-wrap input` 已有 `min-width: 0`。
- `.email-list-top strong` 已有 `overflow: hidden`、`text-overflow: ellipsis`、`white-space: nowrap`。
- `.email-list-meta span` 已有截断。
- `.email-detail-head h2` 已有 `overflow-wrap: anywhere`。
- `.email-meta-grid` 第二列是 `minmax(0, 1fr)`。
- `.email-meta-grid strong/code` 已有 `min-width: 0` 和 `overflow-wrap: anywhere`。
- `1120px` 以下 `.email-workbench` 已切换为单列。
- `760px` 以下 `.email-search-form` 已切换为单列。
- `560px` 以下 `.email-list-top`、`.email-list-meta`、`.panel-header` 已切换为纵向。

---

## 4. 回归样例

### 4.1 邮箱地址样例

普通邮箱：

```text
union@asadad.bond
```

长邮箱：

```text
very-long-mailbox-name-for-ui-overflow-regression-abcdefghijklmnopqrstuvwxyz-0123456789@very-long-subdomain-for-mailhub-layout-testing.example-mailhub-overflow.test
```

### 4.2 Message-ID 样例

普通 Message-ID：

```text
<202607080001.mailhub@example.com>
```

长 Message-ID：

```text
<mailhub-ui-second-optimization-regression-abcdefghijklmnopqrstuvwxyz-0123456789-abcdefghijklmnopqrstuvwxyz-0123456789-abcdefghijklmnopqrstuvwxyz-0123456789@mail.asadad.bond>
```

### 4.3 邮件主题样例

普通主题：

```text
订单通知
```

长主题：

```text
MailHub UI 第二次优化布局回归测试 - 这是一封用于验证邮件列表标题截断和邮件详情标题换行的超长主题 - abcdefghijklmnopqrstuvwxyz - 0123456789 - repeated-repeated-repeated
```

### 4.4 发件人/收件人样例

```text
"Very Long Sender Display Name For Layout Regression abcdefghijklmnopqrstuvwxyz" <sender-with-very-long-local-part-abcdefghijklmnopqrstuvwxyz@example-long-domain.test>
```

```text
receiver-one-with-long-name@example-long-domain.test, receiver-two-with-long-name@example-long-domain.test, receiver-three-with-long-name@example-long-domain.test
```

### 4.5 附件样例

```text
mailhub-ui-second-optimization-attachment-preview-and-layout-regression-abcdefghijklmnopqrstuvwxyz-0123456789.pdf
```

```text
very-long-inline-image-filename-for-email-detail-attachment-list-overflow-regression-abcdefghijklmnopqrstuvwxyz-0123456789.png
```

### 4.6 正文样例

长文本正文：

```text
This is a very long plain text line for MailHub email body overflow regression testing without natural short breaks: abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789
```

Raw 元信息：

```json
{
  "message_id": "<mailhub-ui-second-optimization-regression-abcdefghijklmnopqrstuvwxyz-0123456789-abcdefghijklmnopqrstuvwxyz-0123456789@mail.asadad.bond>",
  "from": "\"Very Long Sender Display Name\" <sender-with-very-long-local-part@example-long-domain.test>",
  "to": [
    "receiver-one-with-long-name@example-long-domain.test",
    "receiver-two-with-long-name@example-long-domain.test"
  ]
}
```

---

## 5. 视口矩阵

O2-P1 至少覆盖以下宽度：

| 宽度 | 目标布局 | 重点观察 |
|------|----------|----------|
| `1366px` | 左右两列 | 列表宽度稳定，详情不挤压列表 |
| `1024px` | 单列或保守单列 | 查询条、列表、详情上下关系清晰 |
| `768px` | 单列 | 列表项、panel header、分页不横向溢出 |
| `390px` | 移动单列 | 页面主体无横向滚动，按钮和标签可读 |

验收时需要确认：

- `document.documentElement.scrollWidth <= window.innerWidth`。
- `.email-workbench` 没有撑开 `.content-main`。
- 只有正文 `<pre>` 或 HTML iframe 内部允许独立滚动。

---

## 6. 状态矩阵

O2-P1 至少覆盖以下状态：

| 状态 | 触发方式 | 预期 |
|------|----------|------|
| 未查询 | 进入 `/admin/emails` | 列表空态和详情空态各在自身 panel 内 |
| 加载中 | 提交邮箱查询 | 加载态只占据列表 panel |
| 查询失败 | 使用不存在或接口异常邮箱 | 错误态不覆盖详情 panel |
| 空列表 | 查询无邮件邮箱 | 空态不跨列、不撑宽 |
| 已选中邮件 | 点击列表项 | 详情就地更新，长字段换行 |
| mailbox 深链 | `/admin/emails?mailbox=<email>` | 自动查询后不滚动到详情 |
| message_id 深链 | `/admin/emails?mailbox=<email>&message_id=<id>` | 列表为空时详情仍清晰展示 |
| 缺 mailbox 的 message_id | `/admin/emails?message_id=<id>` | 错误提示在列表区域内 |

---

## 7. O2-P1 改动边界

O2-P1 应只处理：

- `EmailsPage.jsx` 中必要的结构 class、title、锚点或单列交互。
- `App.css` 中邮件查询工作台相关布局和防溢出样式。

O2-P1 不处理：

- 附件预览。
- 创建邮箱 tab 化。
- 主题品牌色。
- 后端接口。
- 管理员账号配置。

---

## 8. O2-P1 完成标准

进入 O2-P1 后，完成时必须确认：

1. `pnpm run build` 通过。
2. 第 5 节四个视口宽度无页面级横向滚动。
3. 第 6 节核心状态不出现列表、详情、查询区域叠放。
4. 第 4 节长文本样例不会撑开页面。
5. 若无法做真实浏览器截图，需要在完成记录里说明原因，并至少保留代码级证据。

---

## 9. 下一步

开始 **O2-P1 邮件查询工作台布局修复**。

优先 CSS 修复顺序：

1. 补齐 `.email-workbench`、`.email-list-panel`、`.email-detail-panel`、`.email-list-item` 的 `min-width: 0`。
2. 调整 grid 列定义为更柔性的 `minmax(280px, clamp(320px, 34vw, 420px)) minmax(0, 1fr)`。
3. 检查列表标题、附件标签、发件人、时间的收缩优先级。
4. 处理附件列表、正文 `<pre>`、HTML iframe 的内部滚动边界。
5. 用本文视口矩阵和状态矩阵回归。
