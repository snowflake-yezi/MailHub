# 集成邮箱（转发目标）管理设计文档

> 状态：已实现并部署 | 最后校准：2026-07-11

## 1. 背景

当前所有非垃圾邮件统一 SMTP 转发到「集成邮箱」`union@asadad.bond`（需求 §2.3 P0）。该地址**写死在 mail-node `config.yaml` 的 `forward.target_address`**，每次更换需登服务器改配置 + 重启。

用户两个诉求合并解决：
- **可见**：在邮箱管理后台看到集成邮箱（当前完全无展示入口）。
- **可指定**：转发目标支持在后台指定，无需改 mail-node 配置。

粒度决策：**仅全局可配**（一个当前生效目标），不做按规则/按邮箱的细粒度路由。

## 2. 数据模型

### 2.1 新表 `integrated_mailboxes`（转发目标池）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK | 自增 |
| email_address | VARCHAR(191) UNIQUE NOT NULL | 集成邮箱地址，如 `union@asadad.bond` |
| display_name | VARCHAR(191) | 备注，如「主汇总」「备用」 |
| is_active | TINYINT(1) NOT NULL DEFAULT 0 | 是否当前生效的转发目标（**全局唯一为 1**） |
| created_at / updated_at | DATETIME | 时间戳 |

gorm AutoMigrate 建表；`email_address` 唯一索引，`is_active` 普通索引。

### 2.2 动态配置项（复用 `system_configs`）

新增 1 条 seed（`config_store.go`）：

| key | value | category | reloadable |
|-----|-------|----------|-----------|
| `forward.target_address` | `union@asadad.bond` | forward | true |

此 key 是 mail-node 转发目标的**真正生效来源**。`integrated_mailboxes.is_active=true` 的记录与该 key 保持同步（激活时事务写入）。

> SMTP 中继参数（host/user/pass）**不迁入 DB**——不在用户需求范围，且密码进 DB 会被 ConfigPage 暴露。维持 mail-node `config.yaml` 现状。

## 3. API（Session 鉴权，`/api/v1/admin`）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/integrated-mailboxes` | 列出全部集成邮箱 |
| POST | `/integrated-mailboxes` | 新增（email_address + display_name） |
| PUT | `/integrated-mailboxes/:id` | 更新 display_name |
| DELETE | `/integrated-mailboxes/:id` | 删除（active 项阻止并提示） |
| POST | `/integrated-mailboxes/:id/activate` | 设为当前生效 |

### 3.1 activate 联动（核心）

`activate` 在一个**事务**内：
1. `UPDATE integrated_mailboxes SET is_active=0`（全表清零）
2. `UPDATE integrated_mailboxes SET is_active=1 WHERE id=?`
3. `SetConfig("forward.target_address", 该记录.email_address)`

事务提交后，**联动 reload**：照 `notifyFilterReload` 模式，遍历 healthy/degraded 节点 `POST /internal/configs/reload`（复用 `config.go` ReloadNode 的遍历逻辑）。

## 4. mail-node 热加载链路（已存在，复用）

```
后台 activate → 写 system_configs.forward.target_address
             → POST /internal/configs/reload（遍历节点）
mail-node ReloadConfigs (node.go:603) → remoteCfg.Reload() (=PullAll 刷新缓存)
mail-node processFile 发送前 → remoteCfg.GetString("forward.target_address", fallback) 动态读
```

**改动**（让 target 真正热加载，而非启动快照）：
- `forward.Service` 注入 `*config.RemoteConfig`
- `processFile` 发送前动态解析 target，传入 `streamToSMTP`
- `streamToSMTP` 加 `targetAddress` 参数（替代 `cfg.TargetAddress`）
- `main.go` 启动时 `TargetAddress` 初值也走 `remoteCfg.GetString(..., yaml fallback)`

## 5. 前端

`MailboxesPage.jsx` 加第 4 个 tab「集成邮箱」：
- 表格列：地址 / 备注 / 当前生效（✅/—）/ 操作
- 操作：编辑备注、设为当前转发目标、删除
- 新增按钮：录入集成邮箱（地址 + 备注）

`api.js` 加 `integratedMailboxAPI`（list/create/update/remove/activate）。

## 6. 决策记录

| 决策 | 状态 | 说明 |
|------|------|------|
| 集成邮箱 = 转发目标池，独立表 | 已确认 | 与业务邮箱语义不同，不污染 mailbox_accounts |
| 粒度=仅全局可配 | 已确认 | 一个 active 目标，不做按规则/按邮箱路由 |
| 只迁 target_address 进 DB | 已确认 | SMTP 凭据留 mail-node，避免 ConfigPage 暴露密码 |
| 复用 system_configs + reload 链路 | 已确认 | 不新建配置下发机制 |
