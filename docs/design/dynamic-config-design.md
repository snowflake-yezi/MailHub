# 动态配置化设计文档

> 状态：已实现并部署 | 最后校准：2026-07-11

> **范围边界：** 本文的 `system_configs` 是全局运行参数体系；通用节点级 snapshot/override 尚未实现，见 `node-config-visibility-design.md`。

## 1. 背景

当前 email_system（mgmt-system + mail-node）中有 80+ 处硬编码值散布在 Go 源码中，包括超时时间、阈值、开关、默认值等。每次调整都需要改代码 + 重新编译部署。参考「早柚核心」的插件管理 UI 模式，新增一个系统配置管理界面，将这些参数动态化、可视化。

## 2. 参考 UI 模式

早柚核心配置界面特征：
- 左侧导航按功能模块分类
- 主区域表格展示「模块名称 | 是否启用 | 参数配置」→ 每行有启用开关 + 参数配置按钮
- 参数配置 = 点击弹出 Modal，展示该模块的所有可调参数
- 底部「保存配置」按钮，部分变更需「重启生效」

## 3. 数据模型

### 3.1 `system_configs` 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK | 自增主键 |
| config_key | VARCHAR(128) UNIQUE | 配置键，如 `forward.scan_interval` |
| config_value | TEXT | 当前值 |
| value_type | VARCHAR(32) | string / int / bool / duration / json |
| category | VARCHAR(64) | 分组：forward / filter / healthcheck / lifecycle / ... |
| label | VARCHAR(128) | 中文显示名 |
| description | TEXT | 参数说明 |
| default_value | TEXT | 默认值（重置用） |
| reloadable | TINYINT(1) | 0=需重启 1=热加载生效 |
| created_at / updated_at | DATETIME | 时间戳 |

### 3.2 配置分组

| 分组 | 显示名 | 影响节点 |
|------|--------|---------|
| `forward` | 邮件转发引擎 | mail-node |
| `filter` | 过滤引擎 | mail-node |
| `lifecycle` | 生命周期管理 | 双端 |
| `healthcheck` | 健康检查 | mgmt-system |
| `heartbeat` | 心跳上报 | mail-node |
| `session` | 管理会话 | mgmt-system |
| `database` | 数据库连接池 | mgmt-system |
| `maildir` | 邮件存储 | mail-node |
| `general` | 通用参数 | 双端 |

## 4. API 设计

### 4.1 管理后台 API（Session 鉴权）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/v1/admin/configs` | 按 category 分组列出全部配置 |
| GET | `/api/v1/admin/configs/:key` | 获取单个配置 |
| PUT | `/api/v1/admin/configs/:key` | 更新单个配置 |
| POST | `/api/v1/admin/configs/batch` | 批量更新 |
| POST | `/api/v1/admin/configs/:key/reset` | 恢复默认值 |

### 4.2 内部 API（Shared-Secret 鉴权）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/v1/internal/configs` | mail-node 拉取全量配置 |
| POST | `/api/v1/internal/configs/reload` | 通知 mail-node 重载变更项 |

## 5. mail-node 远程配置

mail-node 不直连 DB，通过 mgmt-system API 拉取配置：
1. 启动时调用 `GET /api/v1/internal/configs` 拉取全量配置，存入内存 `sync.Map`
2. mgmt 配置变更后 `POST /api/v1/internal/configs/reload` 通知增量更新
3. `reloadable=true` 的配置项即时生效，其余需重启 mail-node

## 6. 前端页面

### 6.1 路由

- `GET /admin/config` → `config.html`（Session 鉴权）

### 6.2 页面结构

- 侧边栏入口：在所有模板 sidebar 中增加「⚙ 系统配置」
- 主区域：表格（模块名称 / 启用状态 / 参数数量 / 操作按钮）
- Modal：点击「参数配置」弹出，显示该分组下所有参数的表单
- 底部：「保存全部配置」按钮 + 提示（* 标记需重启）

## 7. 实现优先级

| 优先级 | 内容 | 文件数 |
|--------|------|--------|
| P0 | DB 表 + CRUD API + 配置页面 + mgmt-system 核心参数 | ~6 |
| P1 | mail-node 参数拉取 + forward/filter/lifecycle 参数动态化 | ~8 |
| P2 | 热加载 + 模块启用开关 | ~4 |

## 8. 决策记录

| 决策 | 状态 | 说明 |
|------|------|------|
| 使用 KV 表而非结构化表 | 已确认 | 灵活扩展，无需改 schema |
| mail-node 通过 API 拉取配置 | 已确认 | 保持架构干净，mail-node 无需 DB |
| P0 先覆盖 mgmt-system 端 | 已确认 | 可独立验证，风险低 |
