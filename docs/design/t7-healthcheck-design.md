# T7 健康检查与节点心跳 设计文档

> 状态：已评审，实现中 | 日期：2026-06-26 | 决策已确认：D-1 心跳不写 status、D-2 探测失败含 draining 一并降 down、D-4 阈值 30s/3/5/90s、D-3 计数落 DB、D-5 一次探测成功即恢复 | 依据：`REQUIREMENTS_ANALYSIS.md` §2.1.3、`phase3-mgmt-completion-plan.md` §4.4 / §6 T7、2026-06-26 代码审查

---

## 1. 背景与目标

### 1.1 问题

T7 前，服务器可用性判断存在「假健康」与「无摘除」两个核心缺口：

| 信号 | T7 前状态 | 后果 |
|------|----------|------|
| **被动心跳**（node → mgmt） | `startHeartbeat` 是 stub：只 `log.Printf`，**从未真正 POST mgmt**（`client := &gin.DefaultWriter` 是垃圾代码） | mgmt 收不到心跳，`last_heartbeat` 永远为旧值，仪表盘「心跳」列长期无更新 |
| **主动探测**（mgmt → node） | **完全缺失**，无 scheduler | mgmt 无法主动发现 node 不可达，停机后仍把新邮箱分配到死机 |
| **状态机** | 只有 `status` 字段被动写入，无超时降级、无连续失败计数 | 服务器宕机后 `status` 一直停留在 `healthy`，分配器照常选它 |
| **心跳写 status** | `UpdateServerHeartbeat` 强制覆盖 `status` | 若 node 上报 `healthy`，会覆盖 mgmt 探测得出的 `degraded/down`，两套信号打架 |

> 注：历史记忆中「mail-node heartbeat → mgmt 200 ✅」其实**不成立**——filter sync 链路是真打通的，heartbeat 是 stub，从未发请求。

### 1.2 目标

1. mail-node **真实上报心跳**（带 Shared-Secret，刷新 mgmt 的 `last_heartbeat` + `current_load`）。
2. mgmt **主动探测 scheduler**：周期性 `GET mail-node /internal/health`，连续失败降级、彻底失败摘除。
3. mgmt **状态机收敛**：心跳超时兜底 + 主动探测融合，`down/draining` 不参与分配。
4. 仪表盘/服务器页**展示真实状态**（探测时间、失败计数）。

### 1.3 非目标

- 不做多实例 mgmt 的分布式 leader 选举（当前单实例部署，scheduler 单点可接受；多实例时再评估）。
- 不做告警外发（邮件/钉钉），仅落库 + 仪表盘展示（告警规则属运维收尾 O2）。
- 不改 mail-node 的 `/internal/health` 业务逻辑（仅确认其返回值够用）。

---

## 2. 架构概览

```
   ┌────────────────────────────────────────────────────────────┐
   │                    mgmt-system :8080                        │
   │                                                            │
   │  HeartbeatHandler (被动)      HealthScheduler (主动)        │
   │  POST /internal/servers/      ticker 30s ──┐                │
   │        heartbeat                         │ 遍历每台 server │
   │        ▲ X-Internal-Token                ▼                  │
   │        │ 刷新 last_heartbeat           GET http://<api>     │
   │        │           + current_load        /internal/health   │
   │        │                                 X-Internal-Token   │
   │        │                                 timeout 5s         │
   │        │                                       │            │
   │        └──────────────┬───────────────────────┘            │
   │                       ▼                                     │
   │            MailServer 状态机（DB 驱动）                     │
   │   status / last_heartbeat / last_probe_at / probe_fail_count│
   │                       │                                     │
   │                       ▼                                     │
   │   GetHealthyServerForDomain (分配只选 status=healthy)        │
   └───────────────────────┬────────────────────────────────────┘
                           │ X-Internal-Token
   ┌───────────────────────▼────────────────────────────────────┐
   │                  mail-node :8081                            │
   │                                                             │
   │   startHeartbeat goroutine           GET /internal/health   │
   │   ticker HeartbeatInterval(默认60s)  ← Shared-Secret 鉴权   │
   │   POST mgmt /api/v1/internal/                               │
   │        servers/heartbeat                                    │
   │   body: {server_id, status, load, disk_usage,               │
   │          total_messages, version}                           │
   └─────────────────────────────────────────────────────────────┘
```

**双信号融合原则**：
- **被动心跳** = node 主动证明「我还活着 + 我能连到 mgmt」。只刷新 `last_heartbeat` 与 `current_load`，**不写 `status`**。
- **主动探测** = mgmt 主动证明「我能连到 node 的健康端点」。**唯一**负责 `status` 的升降。
- 两者互为佐证：主动探测失败 + 心跳超时 → 坐实 `down`。

---

## 3. 现状盘点（代码事实，2026-06-26）

| 文件 | 现状 | T7 是否改 |
|------|------|-----------|
| `mail-node/cmd/node/main.go:131` `startHeartbeat` | stub，只 log，`client := &gin.DefaultWriter` 无意义 | **重写**为真实 POST |
| `mail-node/internal/handler/node.go:241` `Health` | 已返回 `status/node_id/node_name/total_messages/uptime` | 不改（够用），补充 `version` 字段 |
| `mail-node/internal/config/config.go` | 有 `Management.APIURL/HeartbeatInterval`、`SharedSecret` | 不改；`config.example.yaml` 补 `shared_secret` 示例 |
| `mgmt .../handler/server.go:348` `Heartbeat` | 已实现，但调 `UpdateServerHeartbeat` 覆盖 status | **改**：只更 last_heartbeat + load |
| `mgmt .../store/store.go:140` `UpdateServerHeartbeat` | 强制写 `status` | **改**：去掉 status 写入；新增负载字段 |
| `mgmt .../store/store.go:231` `GetHealthyServerForDomain` | 已要求 `status=healthy` | 不改（天然排除 down/draining） |
| `mgmt .../model/model.go:16` `MailServer` | 有 `Status` + `LastHeartbeat` | **加字段**：`LastProbeAt`、`ProbeFailCount` |
| `mgmt .../cmd/server/main.go` | 无 scheduler | **加**：启动 `HealthScheduler` goroutine |
| `mgmt .../handler/admin.go:21` `Dashboard` | 统计 healthy 数 | 补：展示探测/失败计数 |
| `mgmt template/admin/dashboard.html` | 仅 healthy/down 两色样式 | 补 degraded/draining 样式 + 探测列 |
| `mgmt template/admin/servers.html` | 已有四色 status + 心跳列 | 补：最近探测 + 失败计数列 |

---

## 4. 数据面改动（mail-node）

### 4.1 真实心跳上报

重写 `startHeartbeat(cfg, ...)`：

```go
func startHeartbeat(cfg *config.Config, engine *filter.Engine) {
    interval := cfg.Management.HeartbeatInterval
    if interval <= 0 {
        interval = 60
    }
    client := &http.Client{Timeout: 10 * time.Second}
    url := strings.TrimRight(cfg.Management.APIURL, "/") + "/api/v1/internal/servers/heartbeat"

    // 启动后立即上报一次，缩短冷启动空白期
    beatOnce(client, url, cfg)
    ticker := time.NewTicker(time.Duration(interval) * time.Second)
    defer ticker.Stop()
    for range ticker.C {
        beatOnce(client, url, cfg)
    }
}
```

- **请求**：`POST <api_url>/api/v1/internal/servers/heartbeat`
- **头**：`X-Internal-Token: <cfg.SharedSecret>` + `Content-Type: application/json`
- **Body**：
  ```json
  {
    "server_id": <cfg.Node.ID>,
    "status": "alive",
    "load": <本节点活跃邮箱数>,
    "disk_usage": "<可选，/var/mail 使用率>",
    "total_messages": <Maildir 邮件总数>,
    "node_name": "<cfg.Node.Name>"
  }
  ```
- **容错**：POST 失败仅 `log.Printf` 告警，**不阻塞、不 panic**（心跳是尽力而为，单次失败无所谓）。
- **间隔**：`cfg.Management.HeartbeatInterval`（默认 60s）。建议生产设 30s，与探测周期对齐。

### 4.2 `/internal/health` 微调

`Health` handler 已返回核心字段。补充 `version`（编译期注入或 config）便于排障，其余不变。mgmt 探测只看 HTTP 200 + `code==0`。

### 4.3 配置补全

`mail-node/config.example.yaml` 补 `shared_secret` 示例（实际国际机 config.yaml 已有，example 漏了）：

```yaml
management:
  api_url: "http://127.0.0.1:8080"
  heartbeat_interval: 30
  filter_sync_interval: 3600

shared_secret: "CHANGE-ME-SAME-AS-MGMT"
```

---

## 5. 控制面改动（mgmt）

### 5.1 主动探测 Scheduler

新建 `mgmt-system/internal/healthcheck/scheduler.go`：

```go
type Scheduler struct {
    store        *store.Store
    client       *http.Client   // Timeout 5s
    sharedSecret string
    interval     time.Duration  // 30s
    probeTimeout time.Duration  // 5s
}

func (s *Scheduler) Start(ctx context.Context) {
    // 启动后立即探测一次
    s.ProbeAll()
    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.ProbeAll()
        }
    }
}

func (s *Scheduler) ProbeAll() {
    servers, err := s.store.ListServers()
    if err != nil { log.Printf(...); return }
    for _, srv := range servers {
        s.probeOne(&srv)   // 单台失败不影响其余
    }
}
```

**`probeOne` 单台探测 + 状态机推进**（见 §6）。

### 5.2 main.go 接线

`main.go` 启动 handler 之后、`r.Run` 之前：

```go
sched := healthcheck.NewScheduler(db, cfg.Auth.SharedSecret, 30*time.Second, 5*time.Second)
go sched.Start(ctx)   // ctx 同 forward 那套 context.WithCancel
```

### 5.3 心跳 handler 调整

`server.go:Heartbeat` 改为：
- 只更新 `last_heartbeat` + `current_load`，**不再写 `status`**。
- 调用新签名 `store.UpdateServerHeartbeat(serverID, load int, nodeName string)`（去掉 status 入参）。
- 收到心跳本身可视为「被动可达」证据；但 status 升降仍由 scheduler 决定，避免心跳把已 `down` 的机器刷成 `healthy` 而探测尚未跟上时的短暂错配（下一个探测周期即修正，可接受）。

---

## 6. 状态机与阈值

### 6.1 字段（MailServer 新增）

```go
LastProbeAt    *time.Time `json:"last_probe_at"`
ProbeFailCount int        `gorm:"not null;default:0" json:"probe_fail_count"`
```

> 计数落 DB 而非内存：重启不丢降级进度、仪表盘可见、为未来多实例留口子。

### 6.2 探测状态机（`probeOne`，每 30s 每台）

```
GET http://<api_host>/internal/health  (X-Internal-Token, timeout 5s)
  │
  ├─ 成功 (HTTP 200 且 code==0)
  │     ProbeFailCount = 0
  │     LastProbeAt    = now
  │     if status ∈ {down, degraded}  → status = healthy   // 恢复
  │     if status == draining          → 保持 draining       // 运维意图，探测成功不动
  │     （注：draining 若因连续探测失败被降为 down，恢复后回到 healthy，
  │        运维需重新标记 draining。draining 为软标记，不引入 desired_status）
  │
  └─ 失败 (网络错 / 非200 / code!=0 / 超时)
        ProbeFailCount += 1
        LastProbeAt    = now
        if ProbeFailCount >= 5           → status = down      // 含 draining：真不可达即摘除（D-2）
        else if ProbeFailCount >= 3      → status = degraded
```

### 6.3 心跳超时兜底（同周期顺带检查）

```
if LastHeartbeat != nil && now - LastHeartbeat > 90s:
    if status == healthy                → status = degraded   // 被动心跳也断了，可疑
    if 探测同时失败 (ProbeFailCount >= 3) → status = down       // 双信号坐实
if LastHeartbeat == nil:                  // 从未心跳（node 未部署真实心跳前）
    仅依赖主动探测
```

### 6.4 阈值汇总（可配置化）

| 参数 | 推荐值 | 说明 |
|------|--------|------|
| 探测周期 `interval` | 30s | scheduler tick |
| 探测超时 `probeTimeout` | 5s | 单次 GET |
| 降级阈值 `degradeThreshold` | 连续 3 次 | → degraded |
| 摘除阈值 `downThreshold` | 连续 5 次 | → down |
| 心跳超时 `heartbeatTimeout` | 90s | 兜底判定 |
| 恢复条件 | 探测成功 1 次 | → healthy（防抖由「连续失败才降级」吸收） |

阈值先用常量写在 `healthcheck` 包内，未来抽到 config。

---

## 7. 分配与摘除

- `GetHealthyServerForDomain`（域名感知）已 `WHERE status='healthy'`，**无需改**：`degraded/down/draining` 自动排除。
- `GetHealthyServerWithMinLoad`（旧非域名路径）同理。
- **效果**：服务器 `down` 后，下一个 30s 周期内 status 即变，`Allocate` 不再选中它，外部 API 创建会返回 `no available server`（已有错误路径），不会落到死机。

---

## 8. 仪表盘展示

### 8.1 Dashboard

- 卡片「邮箱服务器（健康/总数）」已存在，补充 `degraded/down` 计数提示。
- 服务器状态表补两列：**最近探测**（`last_probe_at`）、**失败次数**（`probe_fail_count`，>0 标红）。
- `dashboard.html` 补 `.status.degraded` / `.status.draining` 样式（当前只有 healthy/down）。

### 8.2 Servers 页

`servers.html` 已有四色 status + 心跳列。补「最近探测 / 失败」列，便于运维一眼定位「心跳在但探测失败」这类半通状态。

---

## 9. 执行拆分

为可增量验证，分三步：

### T7A — mail-node 真实心跳（数据面）
1. 重写 `startHeartbeat` → 真实 POST + `X-Internal-Token` + 启动即上报。
2. mgmt `Heartbeat` handler 改为只更 `last_heartbeat`+`load`；`UpdateServerHeartbeat` 去 status。
3. `config.example.yaml` 补 `shared_secret`。
4. **验证**：本地起 mail-node + mgmt，观察 mgmt DB `last_heartbeat` 每 30s 刷新、servers 页「心跳」列更新；不带/带错 token → 401。

### T7B — mgmt 主动探测 scheduler（控制面）
1. `MailServer` 加 `LastProbeAt`/`ProbeFailCount`（AutoMigrate）。
2. 新建 `internal/healthcheck` 包 + `Scheduler`。
3. main.go 接线 goroutine。
4. **验证**：停掉某台 mail-node，连续 3 个周期(≈90s) → degraded，5 个周期(≈150s) → down；重启恢复 → healthy；期间外部 API 创建不落到该机。

### T7C — 仪表盘收口 + 文档
1. dashboard / servers 页补探测列与样式。
2. 本设计文档转「已实现」。
3. 更新 `context.md` 决策表 + memory。
4. **验证**：停 node 后仪表盘实时反映 degraded→down；恢复后回 healthy。

---

## 10. 验收清单

- [ ] mail-node 启动后 30s 内，mgmt `mail_servers.last_heartbeat` 被刷新；servers 页显示最新心跳时间。
- [ ] 错误/缺失 `X-Internal-Token` 的心跳被 mgmt 401 拒绝。
- [ ] 停掉某 mail-node：90s 内 mgmt `status=degraded`、150s 内 `status=down`，仪表盘可见。
- [ ] `down` 期间 `POST /api/v1/mailboxes` 不分配到该机（返回 no available server 或落到其他健康机）。
- [ ] 恢复 mail-node：下个 30s 周期 `status` 回 `healthy`，恢复参与分配。
- [ ] 运维手动置 `draining` 的机器，主动探测成功/失败均不自动改其 status（D-2 推荐）。
- [ ] `last_probe_at`、`probe_fail_count` 在仪表盘可见。
- [ ] `go build ./...` 双模块通过；mgmt 重启后 down 状态不丢失（DB 持久）。

---

## 11. 决策点（已确认）

| # | 决策 | 结论 |
|---|------|------|
| **D-1** | 被动心跳是否写 `status`？ | ✅ **否**——心跳只刷 `last_heartbeat`+`load`，status 完全由主动探测决定，消除两套信号打架 |
| **D-2** | `draining` 机器探测失败如何处理？ | ✅ **探测失败也降为 down**——draining 是软标记，真不可达即摘除；恢复后回 healthy，运维需重新标记 draining（不引入 desired_status） |
| **D-3** | 失败计数存内存还是 DB？ | ✅ **DB**（`probe_fail_count` 字段，重启不丢、可观测） |
| **D-4** | 阈值 | ✅ **30s 周期 / 超时 5s / 连续失败 3→degraded / 5→down / 心跳超时 90s**（与 plan §4.4 一致） |
| **D-5** | 恢复条件 | ✅ **1 次探测成功即恢复 healthy**（防抖靠「连续失败才降级」吸收） |

---

## 12. 风险与回滚

| 风险 | 缓解 |
|------|------|
| scheduler 单点（mgmt 挂则无人探测） | mgmt 本身是控制面单点，挂了分配也停；多实例留作后续 |
| 网络抖动误降级 | 连续 3 次才 degraded、5 次才 down，天然吸收抖动 |
| 阈值过激把正常机误摘 | 阈值常量化，便于随时调；draining 手动兜底 |
| 旧 `UpdateServerHeartbeat` 调用方 | 仅 `Heartbeat` handler 一处调用，改动面可控 |

回滚：T7A/B 各自可独立回退（心跳改回 stub、scheduler goroutine 不启动），不影响数据面收发信主链路。
