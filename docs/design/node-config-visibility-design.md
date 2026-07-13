# 节点配置可观测与覆盖设计文档

> **状态：v3 评审修订版（2026-07-13）** ｜ 在 v2 代码事实审计基础上，补齐可靠下发、应用确认、配置版本与实施顺序。
> **v2 起因：** 评审时质疑「清理回收站为什么也要重启节点」，深挖后发现 v1 把 `reloadable` / `requires_restart` 当成「人工填的元数据」，与代码实现严重脱节，并暴露多个真 bug。本次修订以**代码事实**为基准重新定义热加载语义。
> **v3 起因：** v2 仍把「刷新 remoteCfg」与「组件确认应用」连接得不够严密，且错误地把定时 snapshot 当成通知丢失的恢复手段。v3 引入 `desired_revision` / `applied_revision`、组件级 `Apply` 和定时拉取自愈。
> **阅读指引：** 第 3.5 / 3.6 节定义生效语义；第 8 节定义版本模型；第 11–13 节是应用协议、状态机与最终实施顺序。

---

## 0. v2/v3 修订摘要

### 0.1 本次修订纠正了什么

| 项 | v1 的说法 | v2 纠正（以代码为准） |
|----|----------|---------------------|
| `reloadable` 语义 | 配置项的固有属性，人工标记 | **运行期是否存在经过测试、可确认结果的 Apply 路径**，不是固有属性 |
| 「reload 即生效」 | 隐含成立 | **reload 只拉取期望配置**；组件 Apply 成功并提交 `applied_revision` 后才算生效 |
| `trash_retention_hours` | RequiresRestart=true | 实现层固化了（`lifecycle.go:276` 用 `l.trashRetention`），**可改造成热加载**，不该让运维重启 |
| `scan_interval` | reloadable=true | **标记与实现矛盾**：ticker 启动固化（`service.go:128`），reload 后间隔不变 → 标记撒谎 |
| 节点覆盖生效路径 | 改完等节点重启 | **节点覆盖改完根本没触发 reload**（`node_config.go` Put/Delete 无 notify），是 bug |

### 0.2 v2/v3 新增能力

- **NC-10 reload 闭环补全**：节点覆盖变更后主动通知该节点 reload，并准确返回通知结果。
- **NC-11 热加载改造**：把 lifecycle / 转发参数接入组件级 Apply，成功后才上报 applied snapshot。
- **NC-12 重启可观测**：节点上报 `boot_id`，mgmt 据此确认重启是否发生（填补 v1 的重启盲区）。
- **NC-13 作用域选择器**：把「全局配置」与「节点覆盖」统一到系统配置页的同一交互下。
- **NC-14 版本与可靠下发**：统一 schema，增加期望/已应用版本，并通过定时拉取实现最终一致性。

### 0.3 v1 中仍然有效的决策（保留）

- 节点配置入口放在**服务器池**，不并入系统配置主表单的「全局默认」语义。
- 两层数据模型：`server_config_overrides`（覆盖）+ `server_config_snapshots`（实际上报）。
- 来源优先级：`节点覆盖 > 全局 system_configs > mail-node 本地 config.yaml`。
- NC-P0 确认的首个覆盖 key 为 `lifecycle.trash_retention_hours`（单位：小时）。

---

## 1. 问题背景

当前 `system_configs` 解决全局动态配置，但运维真正需要的是**节点维度的真实配置与生效状态**。v1 已解决「能不能看到每节点配置」，v2 进一步解决「看到的配置到底生没生效、改了能不能不重启就生效」。

v2 评审中暴露的**新问题**（详见第 4 节）：

1. 运维在后台改了节点覆盖，但**节点不重启就永远拉不到新值**。
2. 系统配置页标记「扫描间隔支持热加载」，实际 reload 后**间隔根本没变**。
3. `reloadable` 标记整体不可信——它和代码读法脱钩，无法判断一个配置改完是否真生效。
4. 需要重启的配置，mgmt **无从得知节点是否已重启**（无 boot_id），只能从配置差值间接猜。

---

## 2. 设计决策

### 2.1 入口位置

保留 v1 决策：节点配置归属「服务器池」维度，不混入系统配置的全局默认语义。

### 2.2 UX：作用域选择器（v2 新增）

在系统配置页顶部新增「作用域」选择器，把全局与节点覆盖统一到同一交互：

```text
系统配置
作用域：[ 全局默认 ▾ ]   ← 切换为某节点即进入该节点覆盖编辑
...
```

详见第 7 节。服务器池的「配置」按钮作为快捷入口，点选等于在系统配置页预选该节点作用域。

### 2.3 热加载优先（v2 核心原则）

> **凡是能改造成热加载的运行参数，优先改实现，而不是把它扔进「需重启」桶里让运维兜。**

判定标准见 3.5 / 3.6。真正「必须重启」的只有进程级配置（Postfix/Dovecot 域名映射、DKIM、监听端口等），这些不在 mail-node Go 进程的热加载范围内。

---

## 3. 核心概念

### 3.1 全局默认值

来自 `system_configs`。例：`lifecycle.trash_retention_hours = 24`、`forward.scan_interval = 5`。

### 3.2 节点覆盖值

某台服务器单独覆盖全局默认，存于 `server_config_overrides`。

### 3.3 节点实际上报值

mail-node 当前实际生效的配置快照，存于 `server_config_snapshots`，含 `effective_value`、`source`、`reported_at`。

### 3.4 来源优先级

```text
节点显式覆盖 > 全局 system_configs > mail-node 本地 config.yaml 兜底
```

### 3.5 热加载的真正含义（v3 完善）

当前「reload」入口只会让 mail-node 重新 `PullAll` 刷新 `remoteCfg` 内存缓存（`mail-node/internal/config/remote.go:138`），它**不等于生效**。v3 将完整热加载定义为：

```text
拉取 desired config/revision
  → 全量校验
  → 各组件 Apply(old, new)
  → 全部成功后原子提交 applied state/revision
  → 上报 snapshot
```

任一组件失败时不得推进 `applied_revision`，应保留上一份已应用状态并上报错误。组件可以采用不同应用策略：

- **`read_through`**：每次使用时读取已提交的 applied config。
- **`reload_hook`**：reload 时重建 ticker、连接或后台循环，完成后返回成功。
- **`restart_process`**：只能通过进程重启应用。
- **`external_reload`**：由 Postfix/Dovecot 等外部进程的专用 reload 流程应用。

```go
// read-through 示例（当前已生效）—— service.go:99 currentTarget
func (s *Service) currentTarget() string {
    if t := s.remoteCfg.GetString("forward.target_address", ""); t != "" { return t }
    return s.cfg.TargetAddress
}

// 固化派（不生效）—— lifecycle.go:276 用启动时固化的 l.trashRetention
cutoff := time.Now().Add(-l.trashRetention)
```

**结论：热加载不是配置的固有属性，也不等价于实时读取。** 它要求存在明确、经过测试且能确认成功或失败的运行期 Apply 路径。

### 3.6 `reloadable` 的正确定义（v3 完善）

`reloadable` 是 schema 中由实现与测试约束的能力声明：

```text
reloadable = true   ⟺  存在可确认结果的 read_through/reload_hook/external_reload 路径
reloadable = false  ⟺  当前只能 restart_process，或热应用路径尚未实现
```

`apply_strategy` 用来区分具体机制，`requires_restart` 仅表示当前策略为 `restart_process`，不能再混淆「暂未改造」和「技术上必须重启」。这些字段由统一 schema 提供，必须有对应 Apply 测试后才能翻转。

---

## 4. 现状审计：已确认的四个问题及修复状态

### 4.1 ✅ 节点覆盖改完不触发 reload（NC-P0 已修复）

- **现象**：后台改某节点 `trash_retention_hours` 覆盖值，写库成功，但节点行为不变。
- **根因**：`mgmt-system/internal/handler/node_config.go` 的 `PutServerConfig`（:75）/ `DeleteServerConfig`（:111）写完 override 后**没有调用 `notifyNodeReload`**。而 mail-node 只在**启动时** `PullAll` 一次（`mail-node/cmd/node/main.go:53`），运行期不会主动拉取。
- **对比**：全局配置页 reload 按钮（`config.go:207`）、集成邮箱变更（`integrated_mailbox.go:126`）、filter 规则变更（`filter.go:84`）都正确触发了 reload——唯独节点覆盖漏了。
- **修复**：PUT/DELETE 写库后通知目标节点，通知带超时且响应准确返回 `reload_dispatched`；通知失败不回滚已保存配置。

### 4.2 🟡 `scan_interval` 标记与实现矛盾（标记已纠正，热加载待 NC-P2）

- 原实现将 `forward.scan_interval` 标为 `Reloadable: true`；NC-P0 已暂时纠正为 false。
- 但 `service.go:128` 的 ticker 是 `time.NewTicker(s.cfg.ScanInterval)` **启动时固化**，reload 不会重建 ticker。
- **后果**：运维改扫描间隔 → 点 reload → 提示成功 → 实际间隔没变。标记撒了谎。

### 4.3 🔴 `reloadable` 标记整体不可信

由 4.2 推广：当前所有 `reloadable` 标记都是人工填的，没有任何机制保证它与代码读法一致。运维无法据标记判断「改完是否生效」，v1 建立的「热加载/需重启」视觉提示（`ConfigPage.jsx:336`）因此失去了事实基础。

### 4.4 🟠 重启不可观测（无 boot_id）

- `MailServer` 模型（`model.go:16`）只有 `LastHeartbeat` / `LastProbeAt`，**没有** `boot_id` / `started_at`。
- 心跳只能证明「活着」，证明不了「重启过」——快速重启心跳几乎不中断。
- v1 的 `pending_restart` 状态（`node_config.go:150`）是靠 `effective ≠ desired` **差值推断**的，无法区分「没重启」与「重启了但没拉到新配置」，也无法回答运维「我改完到底重启了没」。

---

## 5. 配置热加载清单（v2 新增）

以代码读法为基准，重新核定每个配置项的热加载能力：

| 配置 key | 使用方代码 | 当前读法 | reload 是否生效 | config_store 标记 | v2 目标 | 改造成本 |
|----------|-----------|---------|:---:|---|---|---|
| `forward.target_address` | `service.go:99` currentTarget | 实时读 | ✅ | true | 保持 | — |
| `forward.smtp_user/pass` | `service.go:110` currentSMTPAuth | 实时读 | ✅ | — | 保持 | — |
| `filter.*`（规则） | `engine.StartAutoSync` | 独立同步 | ✅ | true | 保持 | — |
| **`forward.scan_interval`** | `service.go:128` ticker | 固化 | ❌ | false（P0 已纠正） | reload hook + ticker 重建 | 中 |
| `forward.max_email_size` | `service.go:224` s.cfg | 固化 | ❌ | false | 改实时读 | 低 |
| `forward.body_preview_size` | `service.go:224` s.cfg | 固化 | ❌ | false | 改实时读 | 低 |
| **`lifecycle.trash_retention_hours`** | `lifecycle.go:289` currentTrashRetention | read-through | ✅ | true（P0 已完成） | 保持 | — |
| `lifecycle.gc_interval_minutes` | `lifecycle.go:251` ticker | 固化 | ❌ | false | ticker 重建 | 中 |
| `lifecycle.drain_timeout_minutes` | 启动注入 | 固化 | ❌ | false | 改实时读 | 低 |
| `healthcheck.*`（mgmt 侧） | `scheduler.go:82` | 实时读(30s TTL) | ✅ | — | 保持 | — |
| 进程级（域名/DKIM/端口） | Postfix/Dovecot | — | ❌ | — | **真·需重启** | — |

**关键结论**：除「进程级配置」外，mail-node 运行层的参数**几乎全部可以改造成热加载**。v1 让运维为 `trash_retention_hours` 重启节点是本可避免的。

---

## 6. 需求清单（v2 更新）

| 编号 | 需求 | 优先级 | 说明 |
|------|------|--------|------|
| NC-1 | 服务器列表可见配置差异摘要 | P0 ✅ | 已实现 |
| NC-2 | 单节点配置抽屉 | P0 ✅ | 已实现（但硬编码单 key，见 7.3） |
| NC-3 | 展示配置来源 | P0 ✅ | 已实现 |
| NC-4 | 展示实际生效值 | P0 ✅ | 已实现 |
| NC-5 | 节点覆盖生命周期保留 | P0 ✅ | 已实现 |
| NC-6 | 重置为全局默认 | P0 ✅ | 已实现 |
| **NC-7** | 配置下发/热加载状态 | P1 | 状态机需重定义（见第 12 节） |
| NC-8 | 扩展到更多 mail-node 配置 | P1 | 受 NC-2 单 key 硬编码阻塞 |
| NC-9 | 节点配置历史记录 | P2 | 审计 |
| **NC-10** | **节点覆盖变更触发 reload** | **P0（修 bug）** | 补 `notifyNodeReload`，见 11.1 |
| **NC-11** | **运行参数热加载改造** | **P1** | 见第 5、11.2 节 |
| **NC-12** | **重启可观测（boot_id）** | **P1** | 见 8.2、11.4 |
| **NC-13** | **作用域选择器 UX** | **P2** | 见第 7 节 |
| **NC-14** | **统一 schema + 配置版本 + 可靠下发** | **P1（闭环基础）** | 必须先于完整状态机与 UX |

---

## 7. UI 设计：作用域选择器（v2 重写）

### 7.1 现状问题

- `ConfigPage.jsx` 纯全局，无节点维度（`:22` 只调 `configAPI`）。
- `ServersPage.jsx` 的 `NodeConfigDrawer`（`:37-121`）**硬编码单一 key** `lifecycle.trash_retention_hours`（`:35`），既没遍历多 key，也没复用 `ConfigDrawer` 的 `renderInput`。
- `components/` 目录为空，所有子组件内联在 page 文件，无可复用基础。
- 生效语义割裂：全局页有「通知节点重载」按钮，节点抽屉保存后 toast 写死「重启 mail-node 后生效」（`ServersPage.jsx:61,73`），两条生效路径前端没统一。

### 7.2 信息架构

```text
系统配置页
├─ 作用域选择器：[ 全局默认 | mail-node-intl | mail-node-cn | ... ]
│   ├─ 选「全局」  → 编辑 system_configs（现有逻辑）
│   └─ 选某节点   → 编辑该节点覆盖 + 对照实际生效值
├─ 统计 tile：可调参数 / 已热加载 / 待改造 / 需重启
└─ 模块卡片（forward / lifecycle / filter / ...）
    └─ ConfigDrawer（schema 驱动渲染，全局/节点共用）
```

作用域切换时：
- **全局**：数据源 `GET /configs`，保存 `POST /configs/batch`，保存后提示「通知节点重载」。
- **节点**：数据源 `GET /servers/:id/configs`（返回 global + override + effective 三栏对照），保存 `PUT /servers/:id/configs/:key`，保存后**后端自动 reload 该节点**（NC-10），前端据 `requires_restart` 提示「已热加载」或「需重启该节点」。

### 7.3 节点配置抽屉：从单 key 到 schema 驱动

当前 `NodeConfigDrawer` 取 `data.items?.[0]`（`ServersPage.jsx:46`）只渲染一项。v2 要求：

1. 后端 `node_config.go` 的 `nodeConfigDefinitions` 扩展为多 key（按第 5 节清单逐项开放）。
2. 前端把 `ConfigPage.jsx:235-277` 的 `renderInput`（按 `value_type` 分支：bool→toggle / int→number / 长 string→textarea）抽成共享组件 `<ConfigField>`，放 `components/ConfigField.jsx`。
3. 节点抽屉遍历 `items`，每项用 `<ConfigField>` 渲染，并额外展示「全局默认 / 节点覆盖 / 实际生效」三栏对照与 `status` 徽标。

### 7.4 组件抽取（前置重构）

`components/` 目录当前为空。实施 NC-13 前先把以下组件从 page 文件抽出独立文件，供全局/节点复用：

- `ConfigField.jsx`（schema 驱动输入，含 reloadable/需重启徽标）
- `ConfigDrawer.jsx`（参数抽屉壳）
- `NodeConfigDrawer.jsx`（三栏对照 + 覆盖编辑）

---

## 8. 数据模型（v3 更新）

### 8.1 `server_config_overrides`（增加版本语义）

节点覆盖值表。保留唯一索引 `unique(server_id, config_key)`；每次全局配置或节点覆盖变化都为受影响节点推进单调递增的 `desired_revision`。连续修改以最新 revision 为准。

### 8.2 统一配置 schema（NC-14）

配置定义不得继续散落在 `defaultConfigs()`、`nodeConfigDefinitions` 和消费者默认值中。统一 schema 至少包含：

| 字段 | 说明 |
|------|------|
| `key` / `owner` | 配置 key 及所有者（mgmt / mail-node / external） |
| `value_type` / validation / default | 类型、范围、默认值 |
| `node_overridable` / `sensitive` | 是否允许节点覆盖、是否敏感 |
| `apply_strategy` | read_through / reload_hook / restart_process / external_reload |
| `snapshot_provider` | 哪个组件负责确认实际生效值 |

UI、写入校验、节点下发白名单、snapshot 过滤和能力徽标都从该 schema 派生。CI 校验 schema、Apply 注册和测试是否齐全，不尝试通过搜索 `remoteCfg.GetXxx` 猜测能力。

### 8.3 `mail_servers` 新增运行态字段（NC-12/NC-14）

| 字段 | 类型 | 说明 |
|------|------|------|
| `last_boot_id` | VARCHAR(64) | 节点最近一次启动的标识，每次启动变化 |
| `last_started_at` | DATETIME | 节点最近一次启动时间 |
| `desired_revision` | BIGINT | mgmt 期望该节点应用的最新配置版本 |
| `applied_revision` | BIGINT | 节点确认全部组件成功应用的版本 |
| `last_apply_error` | TEXT | 最近一次拉取/校验/Apply 失败摘要 |

用途：mgmt 据此判断节点是否重启过，填补第 4.4 节的重启盲区。心跳接口上报时携带 `boot_id`，mgmt 检测到变化即标记「节点已重启」。

### 8.4 `server_config_snapshots`（语义更新）

snapshot 必须来自组件确认后的 applied state，禁止直接把刚拉取的 desired cache 当成实际值。新增 `desired_revision`、`applied_revision`、`boot_id`、`applied_at`；mgmt 只在 revision 匹配且 effective/source 匹配时判定 `applied`。

对重启项，在配置变更时额外记录 `boot_id_at_change`。只有后续 snapshot 的 `boot_id != boot_id_at_change` 且 revision/value 均匹配，才能确认本次变更已随重启生效。

### 8.5 `system_configs.reloadable`（由 schema 派生）

按 3.6 重定义。数据库中的 `reloadable` 只作为 schema 的投影，不允许成为另一份人工维护的事实源。

---

## 9. API 设计（v3 更新）

### 9.1 管理后台 API（已实现 + 补充）

```http
GET    /api/v1/admin/servers/:id/configs          # 已实现：返回 global/override/effective/source/status
PUT    /api/v1/admin/servers/:id/configs/:key     # 已实现，v2 补：写后自动 notifyNodeReload(该节点)
DELETE /api/v1/admin/servers/:id/configs/:key     # 已实现，v2 补：写后自动 notifyNodeReload(该节点)
POST   /api/v1/admin/configs/reload               # 已实现：全量 reload
```

PUT/DELETE 响应补充：

```json
{ "desired_revision": 42, "requires_restart": false, "reload_dispatched": true, "reload_target": "single" }
```

override 写库成功与 reload 通知成功是两个独立结果。通知使用有明确连接/总超时的 HTTP client；通知失败时配置仍已保存，响应必须返回 `reload_dispatched=false` 和可记录的错误码，状态进入 `pending_retry`，不得忽略错误或假装成功。

### 9.2 内部 API（已实现 + 补充）

```http
GET  /api/v1/internal/configs?server_id=1                  # 已实现：合并全局+覆盖下发
POST /api/v1/internal/servers/:id/config-snapshot           # 已实现
POST /api/v1/internal/configs/reload                        # 已实现：mail-node 侧 ReloadConfigs
```

补充：配置拉取响应返回 `desired_revision`；snapshot 上报 `applied_revision`；心跳增加 `boot_id` 和当前 `applied_revision`，供 mgmt 发现节点落后并重新通知。

---

## 10. 配置合并规则（不变）

```text
1. 读取 system_configs
2. 读取 server_config_overrides where server_id = ?
3. 用 override 覆盖同 key 的 global value
4. 返回合并后的配置给该节点
```

---

## 11. mail-node 行为与热加载改造（v3 重写）

### 11.1 NC-10：补全 reload 闭环（修 bug）

`node_config.go` 的 `PutServerConfig` / `DeleteServerConfig` 写库成功后，调用 `notifyNodeReload(server.APIHost)`（已有单节点通知能力，`config.go:240`）通知该节点 reload。

```go
// 伪代码：PutServerConfig 末尾
if err := h.store.SetServerConfigOverride(...); err != nil { ... }
if server, err := h.store.GetServer(serverID); err == nil {
    reloadErr = h.notifyNodeReload(server.APIHost) // 有超时；结果写入响应/状态
}
```

### 11.2 NC-14：可靠拉取与版本协议（先于批量热加载）

- mgmt 每次变更生成新的 `desired_revision`，拉取接口连同合并后的配置一起返回。
- mail-node 除接收即时 reload 通知外，还要定时 `PullAll`（带随机抖动和失败退避）。通知负责低延迟，定时拉取负责通知丢失后的最终一致性。
- 若拉到的 revision 不高于本地 `applied_revision`，不重复 Apply。
- snapshot 定时上报只用于对账和发现漂移，**不能替代定时拉取或通知重试**。

### 11.3 NC-11：组件级 Apply 改造清单

按 schema 的 `apply_strategy` 注册组件 Apply。简单参数可走 read-through，资源与循环参数必须使用 reload hook：

```go
// lifecycle.go：让 Lifecycle 持有 remoteCfg 引用，每次 GC 实时读
func (l *Lifecycle) purgeExpiredTrash() {
    retention := l.remoteCfg.GetDurationHours("lifecycle.trash_retention_hours", 24*time.Hour)
    cutoff := time.Now().Add(-retention)
    ...
}
```

逐项改造（对应第 5 节）：

- **低成本（read-through）**：`trash_retention_hours`、`max_email_size`、`body_preview_size`、`drain_timeout_minutes`。
- **中成本（需 ticker 重建）**：`scan_interval`、`gc_interval_minutes`。两种方案：
  - 方案 A：reload 时关闭旧 ticker、用新间隔重建（需给 Service/Lifecycle 加 `RestartLoop` 方法）。
  - 方案 B：固定短心跳 ticker（如 1 min），每次 tick 实时读间隔并决定是否执行——实现简单但精度受限。
  - 推荐方案 A，保持语义清晰。

Apply 协调器必须先完成全量校验，再依次/分组应用；任一组件失败时不提交新 `applied_revision`。对无法回滚的组件，需要在实现任务中明确幂等与部分失败恢复策略。

### 11.4 NC-11 配套：Apply 成功后上报 snapshot

`ReportSnapshot` 当前仅在启动调用一次（`main.go:118`）。改造后只能在所有组件 Apply 成功、原子提交 `applied_revision` 后上报；内容从各组件 snapshot provider 读取实际 applied state。仅 `PullAll` 成功不得上报为 applied。

### 11.5 NC-12：boot_id 上报

- mail-node 启动时生成 `boot_id`（进程启动时间的纳秒戳或随机 UUID）。
- 通过心跳接口随每次心跳上报；mgmt 据此维护 `mail_servers.last_boot_id` / `last_started_at`。
- 配置变更时保存 `boot_id_at_change`。检测到不同 boot_id 只进入 `restart_detected`，仍需等待匹配 revision 的 snapshot 才能 applied。

### 11.6 snapshot 上报频率

- **启动时**：必报（已有）。
- **Apply 成功后**：必报。
- **定时**：每 N 分钟对账，发现状态漂移或上报丢失；恢复通知丢失依赖 11.2 的定时拉取。

---

## 12. 状态机（v3 更新）

### 12.1 热加载项（`requires_restart=false`）

```text
unreported ──配置变更/revision++──▶ pending_apply ──Pull+Apply+snapshot──▶ applied
                                          │
                                          ├──通知失败──▶ pending_retry
                                          └──拉取/校验/Apply 失败──▶ apply_failed
```

`pending_apply`：desired revision 已生成，等待节点确认应用；不要求即时通知一定成功。
`applied`：`applied_revision == desired_revision`，且上报的 effective/source 与期望一致。

### 12.2 重启项（`requires_restart=true`，仅进程级配置）

```text
unreported ──覆盖变更──▶ pending_restart ──检测到 boot_id 变化──▶ restart_detected
                              │                                       │
                              └──(超时未重启)──▶ restart_overdue      └──snapshot 校验──▶ applied
```

`pending_restart`：等节点重启。
`restart_detected`：snapshot/心跳的 boot_id 不同于 `boot_id_at_change`，证明变更后发生过重启。
`applied`：重启后上报的 revision、effective、source 均与 desired 一致。
`restart_overdue`：超过阈值（如 `delete_watchdog_minutes` 量级）仍未重启，告警。

> 注意：v2 收敛后，**真正的重启项只剩进程级配置**。lifecycle/forward 运行参数应全部走 12.1 热加载分支。

---

## 13. 实施 Phase（v3 重排）

### NC-P0：止血（已完成，2026-07-13，可独立发版）

- [x] [NC-10] `PutServerConfig` / `DeleteServerConfig` 补 `notifyNodeReload`，设置超时并准确返回通知结果。
- [x] [NC-11a] `trash_retention_hours` 改为运行期读取（最低成本、最高频）。
- [x] [NC-7] 翻转 `scan_interval` 标记为 false（直到 ticker 重建完成），消除「标记撒谎」。
- [x] 前端提示与 `pending_reload` / `reload_dispatched` 实际结果对齐。
- [x] 两个 Go 模块普通测试及 `go test -race ./...`、Web 生产构建通过。

### NC-P1：契约与版本基础（后续能力的前置条件）

- [NC-14] 建立统一配置 schema，替代多处手工元数据。
- [NC-14] 引入 `desired_revision` / `applied_revision` 和 snapshot provider 契约。
- [NC-14] mail-node 定时拉取（抖动 + 退避），形成最终一致性。
- [NC-14] 建立 Apply 协调器及成功/失败测试骨架。

### NC-P2：热应用闭环

- [NC-11] 按第 5 节逐项实现 read-through / reload hook。
- [NC-11] ticker 重建完成并有 Apply 测试后，把能力标记翻回 true。
- [NC-11] Apply 成功后提交 revision 并上报真实 snapshot。

### NC-P3：重启可观测与状态机

- [NC-12] `boot_id` 上报、`boot_id_at_change` 与 mail_servers 字段。
- [NC-7] 落地第 12 节状态机（pending_apply / pending_retry / apply_failed / restart_detected / overdue）。
- [NC-11] snapshot 定时上报用于对账。
- 列表/抽屉展示新状态徽标。

### NC-P4：作用域选择器 UX

- [NC-13] 抽取 `ConfigField` / `ConfigDrawer` / `NodeConfigDrawer` 共享组件。
- [NC-13] `ConfigPage` 顶部加作用域选择器，统一全局/节点交互。
- [NC-8] 基于统一 schema 扩展多 key，节点抽屉 schema 驱动渲染。

### NC-P5：扩展与审计

- [NC-8] 覆盖更多 mail-node 配置。
- [NC-9] 配置变更历史审计。
- [NC-4] CI 校验 schema、Apply 注册、snapshot provider 和测试覆盖一致性。

---

## 14. 验收标准（v2 补充）

### 14.1 修 bug 验收（NC-P0）

- 改某节点 `trash_retention_hours` 覆盖后，**不重启节点**，下次 GC 周期即按新值清理。
- `scan_interval` 标记翻转后，UI 徽标与实际行为一致（要么真热加载，要么标需重启）。

### 14.2 版本与热加载验收（NC-P1/P2）

- 即时通知失败时，节点通过定时拉取最终获得并应用最新 revision。
- 第 5 节全部运行参数按 schema 策略生效，无需重启；ticker 类参数确认旧循环停止且新循环唯一运行。
- 只有组件 Apply 成功且 snapshot 的 `applied_revision == desired_revision` 后才显示 `applied`。
- Apply 失败保留上一 applied revision，显示错误且不得误报新值已生效。

### 14.3 重启可观测验收（NC-P3）

- 改一个真·重启项后，节点未重启时显示 `pending_restart`；节点重启后 `boot_id` 变化，状态推进到 `restart_detected` → `applied`。
- 超时未重启显示 `restart_overdue` 并告警。

### 14.4 安全与可控性（v1 保留）

- 只有 admin session 可编辑节点配置。
- 内部 snapshot/reload 上报必须使用 `X-Internal-Token`。
- 无效值不能保存（如 `trash_retention_hours` ∈ [1, 8760]）。

---

## 15. 风险与取舍（v3 更新）

| 风险 | 说明 | 应对 |
|------|------|------|
| `reloadable` 标记再次失控 | 人工标记易与实现脱节（4.3 已发生） | 统一 schema；CI 校验 Apply 注册、provider 与测试 |
| 热加载改造引入并发问题 | applied state 切换与组件 Apply 存在并发访问 | 复用/扩展 `remoteCfg` 锁，并为 Apply 协调器增加并发测试 |
| ticker 重建竞态 | reload 时重建 ticker 可能丢 tick / 重复执行 | 用 channel 控制单 goroutine 生命周期，reload 发信号重建 |
| reload 通知丢失 | HTTP 通知可能失败 | 定时 PullAll + 抖动/退避实现自愈；snapshot 只负责对账 |
| snapshot 把期望值误报为生效值 | Pull 成功但组件尚未 Apply | snapshot 只读取组件 applied state，revision 匹配后才判定成功 |
| 连续修改/乱序上报 | 旧 snapshot 覆盖新状态 | 单调 revision；mgmt 忽略低于当前 desired/applied 的旧上报 |
| 节点旧版本无 boot_id | 老节点不上报 boot_id | `last_boot_id` 为空时回退到 v1 的差值推断，标注「未知」 |
| 本地 config.yaml 暗配置 | 旧节点仍用本地值 | snapshot 上报 `source=local_config`，逐步迁移（v1 保留） |

---

## 16. 推荐结论

v1 解决了「节点配置可见」，v2 解决「节点配置可信、可热生效、可确认重启」。

三条核心纠正：

1. **`reloadable` 是经过测试的 Apply 能力，不是“是否调用 GetXxx”的人工标签。**
2. **拉取不等于生效**——只有组件 Apply 成功并提交 `applied_revision` 才能上报 applied。
3. **通知负责低延迟，定时拉取负责最终一致性，snapshot 只负责事实对账。**
4. **重启必须与具体变更关联**——用 `boot_id_at_change` + revision 核对，而非只看 last_boot_id 变化。

确认后的实施顺序为：**NC-P0 止血 → NC-P1 schema/版本/可靠拉取基础 → NC-P2 热应用闭环 → NC-P3 boot_id/状态机 → NC-P4 作用域 UX → NC-P5 扩展与审计**。后续阶段不得越过前置阶段：尤其不能在 applied 事实链建立前先做状态徽标和统一 UX。
