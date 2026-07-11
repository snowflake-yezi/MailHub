# 节点配置可观测与覆盖设计文档

> 状态：P1 第一阶段已实现（NC-P0/NC-1 至 NC-6） | 日期：2026-07-11
> 背景：当前系统配置是全局维度，管理后台看不到每个 mail-node 的实际配置与差异。
> 结论：不建议把节点配置直接塞进现有「系统配置」页面；应新增服务器维度的「节点配置」能力，系统配置继续作为全局默认事实源。
> 现状校准（2026-07-10）：系统已有专用节点级 `mail_servers.heartbeat_interval`，但没有通用 override/snapshot 框架。本文示例中的 `lifecycle.retention_days` 不是当前真实配置键；现有 mgmt 默认值是 `general.default_retention_days`，mail-node 删除回收配置是 `lifecycle.trash_retention_hours`。进入 NC-P1/NC-P2 前，必须先确认“邮件保留天数”的业务语义、配置所有权和最终键名。
>
> **术语约束：** 除非明确标为“当前实现”，本文所有 `lifecycle.retention_days`、`heartbeat.interval` 和相关 API JSON 都是设计示意，**不是现有 API、数据库或配置合约**。实施必须先完成 NC-P0，并以确认后的 canonical key 替换示意键；不得机械照抄本文示例。

> **NC-P0 结论（2026-07-11）：** 首个节点覆盖项采用 `lifecycle.trash_retention_hours`，单位小时，表示 mail-node 软删除目录物理清理前的保留窗口，属于节点运行配置且当前需要重启生效。`general.default_retention_days` 仅表示创建邮箱时写入账号的默认业务保留天数，所有权在 mgmt-system，不是节点运行配置，不纳入节点覆盖。核对同时发现 mail-node 曾将 `trash_retention_hours` 按分钟解析，现已改为按小时解析。

---

## 1. 问题背景

当前 `system_configs` 解决的是全局动态配置，例如：

- 转发扫描间隔。
- 生命周期保留/清理策略。
- 健康检查与心跳默认值。
- 会话、分页、容量等后台参数。

但实际运维经常需要看的是**节点维度的真实配置**：

```text
A 节点：邮件清空 / 保留 30 天
B 节点：邮件清空 / 保留 60 天
```

现在管理后台只能看到全局配置，看不到：

- 某个节点当前实际生效值是多少。
- 这个值来自全局默认、本地 `config.yaml`，还是节点覆盖。
- 哪些节点和全局默认不一致。
- 哪些配置需要重启，哪些可以热加载。
- 节点最后一次上报配置是什么时候。

这会导致两个问题：

1. 运维误判：以为全局配置是 30 天，实际某个节点仍按 60 天执行。
2. 排障困难：节点行为异常时，需要 SSH 登录机器看配置文件或日志。

---

## 2. 设计决策

### 2.1 是否放进现有「系统配置」？

不建议直接放进现有「系统配置」作为普通模块。

原因：

- 「系统配置」当前语义是**全局默认配置**，适合表达“默认规则是什么”。
- 节点配置是**服务器实例维度**，天然和服务器状态、心跳、域名池、容量、健康状态绑定。
- 如果混在系统配置里，会把全局默认、节点覆盖、节点实际上报值混成一张表，用户很难判断哪个值真正生效。

### 2.2 推荐信息架构

采用两层结构：

```text
系统配置
  管全局默认值
  例如 <retention-key>.default = 30（目标键，NC-P0 待确认）

服务器池 / 节点配置
  管每个节点的实际值和覆盖值
  例如 server_id=1 <retention-key> = 30（目标键，NC-P0 待确认）
       server_id=2 <retention-key> = 60
```

推荐页面入口：

1. **服务器池列表增加「配置」操作**：点击某台服务器进入该节点配置抽屉。
2. 后续如配置项较多，再新增独立页面：

```text
/admin/servers/:id/configs
```

第一阶段不单独新增顶级菜单，避免侧边栏继续膨胀。

---

## 3. 核心概念

### 3.1 全局默认值

来自 `system_configs`。

例：

```text
<retention-key> = 30               # 目标键，NC-P0 待确认
forward.scan_interval = 5s
mail_servers.heartbeat_interval = 30  # 当前已有的专用节点字段，不是通用 config key
```

### 3.2 节点覆盖值

某台服务器单独覆盖全局默认。

例：

```text
server_id=2
<retention-key> = 60  # 目标键，NC-P0 待确认
```

### 3.3 节点实际上报值

mail-node 当前实际生效的配置快照。

例：

```text
server_id=2
<retention-key> effective_value = 60  # 目标键，NC-P0 待确认
source = server_override
reported_at = 2026-07-08 18:10:00
```

### 3.4 来源优先级

建议优先级：

```text
节点显式覆盖 > 全局 system_configs > mail-node 本地 config.yaml 默认/兜底
```

注意：本地 `config.yaml` 不应长期作为管理后台不可见的“暗配置”。第一阶段可以展示它，后续应尽量迁到可上报/可下发的配置体系。

---

## 4. 需求清单

| 编号 | 需求 | 优先级 | 说明 |
|------|------|--------|------|
| NC-1 | 服务器列表可见配置差异摘要 | P0 | 显示该节点是否存在覆盖、是否与全局不一致、最后上报时间 |
| NC-2 | 单节点配置抽屉 | P0 | 从服务器池进入，查看该节点关键配置 |
| NC-3 | 展示配置来源 | P0 | global / server_override / local_config / unknown |
| NC-4 | 展示实际生效值 | P0 | 不只展示计划值，还要展示 mail-node 上报的实际值 |
| NC-5 | 节点覆盖生命周期保留天数 | P0 | 先覆盖用户最关心的“邮件清空/保留天数” |
| NC-6 | 重置为全局默认 | P0 | 清除节点覆盖，回到系统配置默认值 |
| NC-7 | 配置下发/热加载状态 | P1 | 展示 pending / applied / reload_failed |
| NC-8 | 扩展到更多 mail-node 配置 | P1 | 转发扫描、GC 间隔、附件预览上限等 |
| NC-9 | 节点配置历史记录 | P2 | 审计谁在何时改了什么 |

---

## 5. 第一阶段范围

第一阶段只做最小闭环：

1. 服务器池列表增加配置摘要：
   - 是否有节点覆盖。
   - 生命周期保留天数有效值。
   - 最后配置上报时间。
2. 服务器行增加「配置」按钮。
3. 点击打开 `NodeConfigDrawer`。
4. Drawer 展示：
   - 全局默认值。
   - 节点覆盖值。
   - 节点实际上报值。
   - 配置来源。
   - 是否热加载。
   - 是否需要重启。
5. 支持编辑：
   - NC-P0 确认后的“邮件保留期” canonical key
6. 支持：
   - 保存节点覆盖。
   - 重置为全局默认。

第一阶段不做：

- 不新增顶级侧边栏菜单。
- 不做复杂配置历史审计。
- 不一次性覆盖全部 `system_configs`。
- 不把所有 mail-node `config.yaml` 项都迁到 DB。

---

## 6. UI 设计

### 6.1 服务器池列表

在服务器池表格中增加一列或摘要区域：

```text
节点配置
  保留 30 天
  跟随全局 / 已覆盖 / 未上报
```

展示规则：

| 状态 | 展示 |
|------|------|
| 跟随全局 | `保留 30 天 · 跟随全局` |
| 节点覆盖 | `保留 60 天 · 已覆盖` |
| 未上报 | `未上报配置` |
| 下发失败 | `配置失败` danger tag |

操作区增加图标按钮：

```text
配置
```

### 6.2 节点配置 Drawer

结构：

```text
节点配置
mail-node-intl · 141.11.2.143:8081

配置概览
  最后上报：2026-07-08 18:10:00
  配置状态：已应用
  覆盖项：1

生命周期
  邮件保留天数
  全局默认：30
  节点覆盖：60
  实际生效：60
  来源：节点覆盖
  [输入框] [重置为全局默认]

保存
```

### 6.3 与系统配置页的关系

系统配置页继续展示全局默认：

```text
生命周期管理
  默认邮件保留天数：30
```

可以在说明文案中增加：

```text
单个服务器可以在「服务器池 > 配置」中覆盖此默认值。
```

---

## 7. 当前数据模型

### 7.1 `server_config_overrides`

节点覆盖值表。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK | 自增 |
| server_id | BIGINT | mail_servers.id |
| config_key | VARCHAR(128) | 配置键 |
| config_value | TEXT | 覆盖值 |
| value_type | VARCHAR(32) | int / bool / duration / string / json |
| updated_by | VARCHAR(128) | 操作人，第一阶段可为空 |
| created_at / updated_at | DATETIME | 时间 |

唯一索引：

```text
unique(server_id, config_key)
```

### 7.2 `server_config_snapshots`

节点实际上报快照表。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK | 自增 |
| server_id | BIGINT | mail_servers.id |
| config_key | VARCHAR(128) | 配置键 |
| effective_value | TEXT | mail-node 实际生效值 |
| source | VARCHAR(32) | global / server_override / local_config / unknown |
| reloadable | BOOL | 是否热加载 |
| requires_restart | BOOL | 是否需要重启 |
| applied_at | DATETIME | 节点应用时间 |
| reported_at | DATETIME | 节点上报时间 |

唯一索引：

```text
unique(server_id, config_key)
```

### 7.3 是否直接扩展 `system_configs`

不建议。`system_configs` 继续保持全局 KV。

如需建立默认值和节点覆盖值的关系，通过相同 `config_key` 关联即可。

---

## 8. API 设计

> 以下接口已在 P1 第一阶段实现。当前唯一开放覆盖的 canonical key 为 `lifecycle.trash_retention_hours`；后续配置项继续复用相同契约。

### 8.1 管理后台 API

```http
GET /api/v1/admin/servers/:id/configs
```

返回：

```json
{
  "server_id": 1,
  "server_name": "mail-node-intl",
  "reported_at": "2026-07-08T18:10:00+08:00",
  "items": [
    {
      "key": "<retention-key>",
      "label": "邮件保留天数",
      "value_type": "int",
      "global_value": "30",
      "override_value": "60",
      "effective_value": "60",
      "source": "server_override",
      "reloadable": true,
      "requires_restart": false,
      "status": "applied"
    }
  ]
}
```

```http
PUT /api/v1/admin/servers/:id/configs/:key
```

请求：

```json
{
  "value": "60"
}
```

```http
DELETE /api/v1/admin/servers/:id/configs/:key
```

语义：删除节点覆盖，恢复跟随全局。

### 8.2 内部 API

mgmt 下发给 mail-node：

```http
GET /api/v1/internal/configs?server_id=1
```

返回全局默认 + 节点覆盖合并后的配置。

mail-node 上报快照：

```http
POST /api/v1/internal/servers/:id/config-snapshot
```

请求：

```json
{
  "reported_at": "2026-07-08T18:10:00+08:00",
  "items": [
    {
      "key": "<retention-key>",
      "effective_value": "60",
      "source": "server_override",
      "reloadable": true,
      "requires_restart": false,
      "applied_at": "2026-07-08T18:09:40+08:00"
    }
  ]
}
```

---

## 9. 配置合并规则

mgmt 内部生成下发配置时：

```text
1. 读取 system_configs
2. 读取 server_config_overrides where server_id = ?
3. 用 override 覆盖同 key 的 global value
4. 返回合并后的配置给该节点
```

示例：

| key | global | server 1 override | server 1 effective |
|-----|--------|-------------------|--------------------|
| `<retention-key>` | 30 | - | 30 |
| forward.scan_interval | 5s | - | 5s |

| key | global | server 2 override | server 2 effective |
|-----|--------|-------------------|--------------------|
| `<retention-key>` | 30 | 60 | 60 |
| forward.scan_interval | 5s | - | 5s |

---

## 10. mail-node 行为

第一阶段建议：

1. mail-node 拉取配置时带上自身 `server_id`。
2. mgmt 返回合并配置。
3. mail-node 应用配置后，上报关键配置快照。
4. 如果某项配置仍来自本地 `config.yaml`，也要上报 `source=local_config`。

候选重点配置（不代表当前已支持）：

```text
<retention-key>（NC-P0 待确认）
lifecycle.gc_interval_minutes
lifecycle.drain_timeout_minutes
forward.scan_interval
forward.max_email_size
mail_servers.heartbeat_interval（已有专用字段）
```

第一阶段可只真正支持：

```text
NC-P0 确认后的“邮件保留期” canonical key
```

其他项先只展示全局/未上报状态。

---

## 11. 实施 Phase

### NC-P0：文档与现状核对

- 明确系统配置 vs 节点配置的边界。
- 确认现有 `system_configs` key 命名。
- 确认 mail-node 当前哪些配置已远程拉取，哪些仍只在本地文件。
- 确认邮件保留期的业务语义、所有权与 canonical key，并记录替换本文示意键的决定。

### NC-P1：只读可观测

- 新增 snapshot 表。
- mail-node 上报关键配置快照。
- 服务器列表展示配置摘要。
- 节点配置 Drawer 只读展示。

### NC-P2：节点覆盖

- 新增 override 表。
- 后台支持编辑 NC-P0 确认后的“邮件保留期” canonical key。
- mgmt 内部配置接口按 server_id 合并全局 + 覆盖。
- 支持重置为全局默认。

### NC-P3：热加载与状态

- 覆盖值变更后通知节点 reload。
- 展示 applied / pending / failed。
- 对不可热加载配置提示需重启。

### NC-P4：扩展配置项与审计

- 覆盖更多 mail-node 配置。
- 增加配置修改历史。
- 增加“所有节点配置差异”汇总视图。

---

## 12. 验收标准

### 12.1 只读阶段

- 服务器池能看到每个节点的保留天数实际值。
- A 节点 30 天、B 节点 60 天时，列表可以直接看出差异。
- 节点配置 Drawer 能看到：
  - 全局默认。
  - 节点实际值。
  - 来源。
  - 最后上报时间。

### 12.2 覆盖阶段

- 在 B 节点设置 NC-P0 确认后的“邮件保留期”值为 60 天后，该节点实际保留 60 天。
- 删除 B 节点覆盖后，B 节点恢复跟随全局 30 天。
- 修改 A 节点不影响 B 节点。
- 修改全局默认不覆盖已有节点显式覆盖。

### 12.3 安全与可控性

- 只有 admin session 可以编辑节点配置。
- 内部快照上报必须使用 `X-Internal-Token`。
- 无效值不能保存，例如保留天数小于 1。

---

## 13. 风险与取舍

| 风险 | 说明 | 应对 |
|------|------|------|
| 配置来源变复杂 | global / override / local_config 混合后用户可能困惑 | UI 明确显示来源和实际生效值 |
| 覆盖值和全局值冲突 | 全局改为 45，但 B 节点仍覆盖 60 | 列表显示“已覆盖”，Drawer 显示全局与覆盖差异 |
| 节点未上报 | 节点离线或旧版本不支持 snapshot | 显示“未上报”，不假装知道实际值 |
| 过早支持所有配置 | 范围膨胀 | 第一阶段只做生命周期保留天数 |
| 本地 config.yaml 仍有暗配置 | 旧节点可能继续用本地配置 | snapshot 上报 `source=local_config`，逐步迁移 |

---

## 14. 推荐结论

建议新增 **节点配置** 能力，入口放在 **服务器池**，不要并入现有系统配置主表单。

推荐产品语义：

```text
系统配置 = 全局默认
节点配置 = 单台服务器的覆盖值与实际生效值
```

第一阶段先做只读可观测和生命周期保留天数：

```text
服务器池列表直接看到：
A 节点：保留 30 天 · 跟随全局
B 节点：保留 60 天 · 已覆盖
```

这样最贴近当前痛点，也不会把系统配置页变成一张难以理解的“大杂烩”。
