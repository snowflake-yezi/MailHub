# T4+T5 服务器邮局管理（宝塔式）— 设计文档

> 版本: v0.3 | 日期: 2026-06-24 | 状态: **已实现并验证：T4A→T4B→T5A→T5B→T5C 闭环完成**
> 依据: `REQUIREMENTS_ANALYSIS.md` §1.2 / §2.1.1 / §2.1.3 / §2.1.6
> 定位: 把「宝塔邮局管理器」的**域名管理 + 域名下邮箱 CRUD** 能力植入本系统，并多机化

---

## 1. 目标与定位

参考宝塔邮局管理器，做**服务器为中心**的邮局管理：

- 每台邮箱服务器管理自己的**域名池**（这台机器为哪些域名提供收发服务）
- 给服务器**添加域名**时，真正让该机能收发该域邮件（配 Postfix 虚拟域 + 生成 DKIM + 给出待解析 DNS 记录）—— 等价于宝塔「添加域名」
- 在**「服务器 → 域名」上下文里创建/批量创建邮箱**
- 创建/分配按**域名感知**：某域邮件只落到绑定了该域的健康服务器（需求 §2.1.3 负载策略）

> 多机化的关键差异：宝塔是单机（域名天然归属本机）；我们是 N 台服务器，故用 `server_domains` M:N 绑定表表达「哪台机服务哪个域」。一个域可被多台机服务（跨机容灾）。

### 1.1 已定决策（2026-06-24 评审）
| # | 决策 | 选择 |
|---|------|------|
| D1 | 添加域名的数据面配置深度 | **Postfix 虚拟域 + DKIM 自动生成 + 返回 DNS 记录**（完整宝塔式），但允许分阶段落地 |
| D2 | 交付范围 | **T4+T5 合并设计、分阶段实施**（先 mgmt 域名池与分配，再补 mail-node 域名数据面闭环） |
| D3 | 域名实体模型 | 全局 `Domain` 表 + `server_domains` M:N（需求 §2.1.3 已确认可跨机共享） |
| D4 | 创建入口范式 | 服务器为中心（用户：「在服务器中创建/批量创建邮箱」） |
| D5 | 多机 DKIM | **每台服务器独立 selector**（如 `mail-s2`、`mail-s3`），避免同一域多机共享 `mail._domainkey` 冲突 |
| D6 | 同步状态 | `server_domains` 必须记录远端 Postfix/DKIM 同步状态；分配器只使用已同步成功的 active 绑定 |

---

## 2. 现状基线（2026-06-24 核查）

**国际机 203.0.113.10**（SSH 免密可查）：
- `virtual_mailbox_domains = example.com` —— **仅一域**，加新域必须改此值
- `virtual_mailbox_maps = hash:/etc/postfix/vmailbox` —— 按邮箱记录
- DKIM **按域硬编码**：`SigningTable`/`KeyTable` 仅 `example.com` 一条，selector=`mail`，key 在 `/etc/opendkim/keys/example.com/`
- opendkim milter `inet:8891@localhost`，`SigningTable refile:...`

**mail-node**（数据面）：
- `mailbox.Manager.Create` 只写 Dovecot `users.conf` + Postfix `vmailbox` + 建目录 + `postmap`/`postfix reload`
- **从不操作 `virtual_mailbox_domains` 和 DKIM** —— 这是 T4+T5 要补的数据面新能力
- 路由注册在 `NodeHandler.RegisterRoutes` 的 `/internal` 组；handler 注入各 manager

**mgmt**（控制面）：
- `Domain` 全局表（example.com，seed 而来）；无 `server_domains`
- 创建邮箱：`MailboxCreator.selectDomain`（全局选 active 域）+ `selectServer`（全局/指定，不按域名）
- 服务器管理页：仅服务器 CRUD，无域名池概念

---

## 3. 数据面设计（mail-node）— 新增「域名」模块

对标宝塔「添加域名」。新建 `internal/domain/manager.go` + 在 `handler` 加域名接口。

### 3.1 DomainManager

```go
type Config struct {
    Selector        string // 默认 selector 前缀，如 "mail"
    KeyDir          string // "/etc/opendkim/keys"
    SigningTable    string // "/etc/opendkim/SigningTable"
    KeyTable        string // "/etc/opendkim/KeyTable"
    PublicHost      string // 该服务器 MX 建议值，如 "mail.example.com"
}
type Manager struct { cfg Config; mailboxMgr *mailbox.Manager }
```

> `PublicHost` 是**服务器级配置**，不是全局域名配置。当前只有 `mail-node-intl` 时可以是 `mail.example.com`；后续第二台服务器应使用自己的公网主机名。mgmt 侧也应在 `mail_servers` 中记录/展示该值，避免生成 DNS 记录时误用第一台服务器的主机名。

**AddDomain(domain) → DomainSetup**：
1. **Postfix 虚拟域**：`postconf -h virtual_mailbox_domains` 读现状 → 按逗号解析去重 → 追加新域 → `postconf -e "virtual_mailbox_domains = a, b, new"` → `postfix reload`
2. **DKIM 生成**：为每台服务器生成唯一 selector，建议 `mail-s<server_id>` 或配置项指定；`opendkim-genkey -D <keydir>/<domain> -d <domain> -s <selector>` → 产出 `<selector>.private` + `<selector>.txt` → `chown -R opendkim:opendkim`
3. **注册 opendkim**：追加 SigningTable 行 `*@<domain> <selector>._domainkey.<domain>`；追加 KeyTable 行 `<selector>._domainkey.<domain> <domain>:<selector>:<keydir>/<domain>/<selector>.private` → `systemctl reload opendkim`
4. **返回 DNS 记录清单**（从 `<selector>.txt` 读 DKIM 公钥；mgmt 页面会按用户填写的 A 记录主机头补全/重写最终 DNS 清单）：
   ```json
   {
     "domain": "new.com",
     "dkim_selector": "mail-s2",
     "dns_records": [
       {"type":"A",   "host":"mail.new.com",            "value":"203.0.113.10"},
       {"type":"MX",  "host":"new.com",                 "value":"mail.new.com"},
       {"type":"TXT", "host":"new.com",                 "value":"v=spf1 a mx ~all"},
       {"type":"TXT", "host":"mail-s2._domainkey.new.com", "value":"v=DKIM1; k=rsa; p=MIGf..."},
       {"type":"TXT", "host":"_dmarc.new.com",          "value":"v=DMARC1; p=quarantine"}
     ]
   }
   ```

**ListDomains()**：`postconf -h virtual_mailbox_domains` 解析返回数组。

**RemoveDomain(domain)**：校验 `vmailbox` 无该域任何记录（`mailboxMgr` 扫描，有邮箱则拒绝）→ 从 virtual_mailbox_domains 移除 → 删 SigningTable/KeyTable 对应行 → `postfix reload` + `systemctl reload opendkim`。

### 3.2 接口（挂 `/internal` 组）
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/internal/domains` `{domain}` | AddDomain，返回 DNS 记录 |
| GET | `/internal/domains` | ListDomains |
| DELETE | `/internal/domains/:domain` | RemoveDomain（有邮箱拒绝） |

### 3.3 config.yaml 增量
```yaml
dkim:
  selector: "mail"
  key_dir: "/etc/opendkim/keys"
  signing_table: "/etc/opendkim/SigningTable"
  key_table: "/etc/opendkim/KeyTable"
public_host: "mail.example.com"
```

**状态语义**：
- `postfix_status=synced` 表示该服务器已接受该虚拟域，收信可用。
- `dkim_status=synced` 表示 DKIM key、SigningTable、KeyTable 已配置并 reload 成功。
- 如果 Postfix 成功但 DKIM 失败，接口仍可返回成功但必须标记 `dkim_status=sync_failed`，mgmt 页面展示为“收信可用、发信签名待修复”。是否允许进入分配池以 `postfix_status` 为准；发信信誉相关告警以 `dkim_status` 为准。

---

## 4. 控制面设计（mgmt）

### 4.1 模型
新增 `ServerDomain`（M:N 绑定 + 远端同步状态 + DNS/DKIM 展示数据）：
```go
type ServerDomain struct {
    ID              uint64     `gorm:"primaryKey;autoIncrement"`
    ServerID        uint64     `gorm:"not null;uniqueIndex:uk_srv_dom"`
    DomainID        uint64     `gorm:"not null;uniqueIndex:uk_srv_dom;index:idx_dom"`
    Status          string     `gorm:"type:enum('active','inactive');default:active;index"`
    SyncStatus      string     `gorm:"type:enum('pending','synced','partial','sync_failed');default:pending;index"`
    SyncError       string     `gorm:"type:text"`
    DkimSelector    string     `gorm:"size:64"`  // 多机共享域名时必须不同，如 mail-s2
    DkimPublicKey   string     `gorm:"type:text"` // AddDomain 返回的 DKIM TXT 值，便于页面展示/复制
    PostfixStatus   string     `gorm:"type:enum('pending','synced','sync_failed');default:pending"`
    DkimStatus      string     `gorm:"type:enum('pending','synced','sync_failed');default:pending"`
    SyncedAt        *time.Time
    CreatedAt       time.Time
    UpdatedAt       time.Time

    Server          MailServer `gorm:"foreignKey:ServerID"`
    Domain          Domain     `gorm:"foreignKey:DomainID"`
}
```
`Domain` 表保持现有 `name/mx_server/status` 可先不动；删除域名使用 `inactive`，不硬删，避免破坏 `mailbox_accounts.domain_id` 和 `server_domains.domain_id` 的历史引用。

建议在 `MailServer` 增加或复用一个服务器公网主机名字段：
- MVP 可复用 `smtp_host` 或 `api_host` 推导，但不推荐长期这样做。
- 推荐新增 `public_host`，用于生成 MX/SPF/DKIM 说明中的主机名。当前 `mail-node-intl` 为 `mail.example.com`。

### 4.2 store 增量
- `BindServerDomain(serverID, domainID, setup)` / `MarkServerDomainSyncFailed(...)` / `UnbindServerDomain(serverID, domainID)`
- `ListDomainsByServer(serverID)` / `ListServersByDomain(domainID)`（preload Domain/Server）
- `GetHealthyServerForDomain(domainID)`：`WHERE domain_id=? AND server_domains.status=active AND server_domains.sync_status IN ('synced','partial') AND server_domains.postfix_status='synced' JOIN mail_servers status=healthy AND current_load<capacity ORDER BY current_load` —— 域名感知分配

### 4.3 「添加域名到服务器」API
`POST /api/v1/admin/servers/:id/domains {name}`：
1. 全局 `Domain` 不存在则创建（`mx_server=mail.<name>`, status=active）
2. 先 upsert `ServerDomain(server_id, domain_id, sync_status=pending)`，保证失败可追踪
3. 调 mail-node `POST /internal/domains {domain}` → 拿 DKIM selector、公钥、DNS 记录、postfix/dkim 状态
4. 根据远端返回更新 `ServerDomain`：
   - `postfix_status=synced && dkim_status=synced` → `sync_status=synced`
   - `postfix_status=synced && dkim_status=sync_failed` → `sync_status=partial`
   - `postfix_status=sync_failed` 或调用失败 → `sync_status=sync_failed`，记录 `sync_error`
5. 返回 DNS 记录给前端展示（运营去 DNS 控制台解析）

配套：`GET /api/v1/admin/servers/:id/domains`（域名池）、`DELETE /api/v1/admin/servers/:id/domains/:domain_id`（移除，校验该机该域无邮箱 → 调 mail-node RemoveDomain）。

**移除域名规则**：
- 如果 `mailbox_accounts` 中存在 `server_id + domain_id` 的账号，拒绝移除。
- 远端 RemoveDomain 成功后，将 `ServerDomain.status=inactive` 或删除绑定；建议 MVP 软停用，保留同步记录。
- 对 `Domain` 全局表不做硬删，仅当所有服务器都不再绑定时可置为 `inactive`。
- **重新添加同名域名**（命中 `inactive` 绑定，2026-06-25 修复）：`BindServerDomain` 用 `Assign(...).FirstOrCreate`，命中旧记录时把 `status` 拉回 `active` 并将 `sync_status/postfix_status/dkim_status` 重置为 `pending`、清空 `dkim_selector/dkim_public_key`，再由 `AddServerDomain` 调远端刷成最新结果；远程失败时落 `active + sync_failed`（不再静默卡 `inactive`）。
- **状态语义**：`server_domains` 的 `sync_status/postfix_status/dkim_status` 是「添加域名那次」远端操作的**本地快照**，非实时查远程；无定时 reconcile，刷新只能靠重新 add。主动探测属 T7（未实现）。

### 4.4 服务器管理页改造（宝塔式 UI）
- 服务器列表每行新增「域名池」入口；域名池页面是 T4/T5 的主入口，优先级高于单独的全局域名 CRUD 页。
- **域名池子页**（`/admin/servers/:id/domains`）：
  - 该服务器已绑定域名列表（域名、DKIM 公钥复制、DNS 状态、邮箱数、移除）
  - 「添加域名」表单：输入域名 → 调接口 → **弹出返回的 DNS 记录清单（MX/SPF/DKIM/DMARC）+ 逐条复制**，提示去 DNS 解析
  - 每个域名下：「创建邮箱」「批量创建」（server_id+domain_id 已固定，只输前缀/密码）→ 复用 `MailboxCreator`
- 导航：在「服务器管理」下展开「域名池」，或服务器页内 tab

### 4.5 邮箱创建（服务器视角）
- 创建入口固定 `server_id` + `domain_id`（来自域名池上下文），只输 prefix/password
- 复用 `MailboxCreator.Create(MailboxCreateInput{DomainID, ServerID, ...})` —— **不改创建器主流程**
- `MailboxCreator.selectServer` 增强：指定 `domainID` 且未指定 `serverID` 时，走 `GetHealthyServerForDomain(domainID)`（域名感知分配）
- 如果指定了 `serverID + domainID`，创建器必须校验该服务器域名绑定存在且 `postfix_status=synced`，否则拒绝创建，避免 mgmt 落库但远端 Postfix 不收该域。

### 4.6 外部 API
`POST /api/v1/mailboxes` 增 `domain_id`（可选）：传了则按域名感知分配服务器；不传维持现状（全局第一个 active 域）。

---

## 5. 关键技术点与风险

| 点 | 约束 / 处理 |
|----|------------|
| **动 virtual_mailbox_domains 是远端关键配置** | `postconf -e` 原子写 main.cf；AddDomain 幂等（域已存在则跳过该步，不重复）；操作前后日志记录 |
| **DKIM 生成失败不阻断收信** | 步骤 1（虚拟域）成功即返回可用收信域；DKIM/opendkim 失败降级为「收信可用、发信未签名」，在返回里标注 `dkim_status` |
| **同一域多机 DKIM 冲突** | 不同服务器不能都使用 `mail._domainkey.<domain>`；selector 需按服务器区分，或后续改为共享 key。MVP 采用按服务器 selector。 |
| **server_domains 状态误用** | 分配器只能使用 `status=active` 且 `postfix_status=synced` 的绑定；DKIM 失败可 partial，但必须在 UI 告警 |
| **MX/A 主机来源** | 添加域名时用户输入原始域名 + A 记录主机头（默认 `mail`）；mgmt 生成 `A <host>.<domain> -> 服务器 IP` 和 `MX <domain> -> <host>.<domain>`，DKIM 仍挂在 `<selector>._domainkey.<domain>` |
| **RemoveDomain 安全** | 必须先扫 `vmailbox` 确认该域无邮箱，有则拒绝（保护存量账号） |
| **opendkim 服务名/权限** | CentOS 7 服务名 `opendkim`，reload 用 `systemctl reload opendkim`；key 目录须 `chown opendkim:opendkim` |
| **DNS 记录需人工解析** | 系统只生成并展示 A/MX/SPF/DKIM/DMARC 文本 + 复制；实际到 DNS 控制台发布是运营动作（与 example.com 当初一致） |
| **mail-node 重新部署** | 数据面改动需交叉编译 ELF → scp 国际机 → systemctl restart mail-node |

---

## 6. 实施步骤（确认后逐项执行）

### T4A：mgmt 域名池地基（不先动远端配置）
1. model 加 `ServerDomain`；AutoMigrate 建 `server_domains`。✅
2. store 加绑定/查询/域名感知分配方法。✅
3. 种子/迁移：把当前 `mail-node-intl(server_id=2) + example.com(domain_id=1)` 写为 `status=active`、`sync_status=synced`、`postfix_status=synced`、`dkim_status=synced`，因为国际机当前已真实配置该域。✅
4. handler 加 `GET /api/v1/admin/servers/:id/domains`，服务器页增加「域名池」入口和只读列表。✅

### T4B：域名池下创建邮箱 + 域名感知分配
5. 域名池页增加该服务器该域下的单个/批量创建入口，固定传 `server_id + domain_id`。✅
6. `MailboxCreator.selectServer` 域名感知：未指定 server 但指定 domain 时走 `GetHealthyServerForDomain(domainID)`。✅
7. 外部 `POST /api/v1/mailboxes` 增可选 `domain_id`；不传时兼容旧默认域。✅
8. 创建器校验指定 server 是否绑定指定 domain 且 `postfix_status=synced`。✅

### T5A：mail-node 域名数据面（Postfix 虚拟域）
9. 新建 `internal/domain/manager.go`（AddDomain/ListDomains/RemoveDomain，先覆盖 Postfix virtual_mailbox_domains + DNS 记录）。✅
10. `internal/handler` 加域名接口，`RegisterRoutes` 挂 `/internal/domains*`。✅
11. 交叉编译 → scp 国际机 → 改 config.yaml → restart mail-node。✅

### T5B：DKIM 自动生成与 DNS 记录
12. mail-node config 加 `dkim`/`public_host`/selector 策略，注入 DomainManager。✅ 代码完成（2026-06-24）
13. AddDomain 增加 DKIM key 生成、SigningTable/KeyTable 写入、opendkim reload。✅ 代码完成（2026-06-24）
14. mgmt 添加域名接口展示 MX/SPF/DKIM/DMARC 记录，并保存 `dkim_selector/dkim_public_key/dkim_status`。✅ 代码完成（2026-06-24，另透传 `dkim_error` 到 `sync_error`）

### T5C：移除域名与回归
15. 实现 `DELETE /internal/domains/:domain`，有邮箱拒绝。✅ 已完成（2026-06-24）
16. mgmt `DELETE /api/v1/admin/servers/:id/domains/:domain_id` 做账号占用校验 + 远端移除 + 本地 inactive。✅ 已完成（2026-06-24）
17. 构建 + 重启本地 mgmt + 部署 mail-node + 跑验证清单。✅ 已完成（2026-06-24）

---

## 7. 验证清单

**T4A/T4B（mgmt）**
- [x] AutoMigrate 建 `server_domains` 表，当前 `mail-node-intl + example.com` 自动绑定为 synced
- [x] 服务器页 → 国际机「域名池」能看到 `example.com`，展示 `postfix_status/dkim_status/sync_status`
- [x] 域名池下「批量创建」固定 `server_id=2 + domain_id=1` → 落 `mailbox_accounts` + 远端真实创建
- [x] 外部 `POST /api/v1/mailboxes {order_id, domain_id}` → 落到绑定该域的健康服务器
- [x] 指定未绑定该域的 server 创建 → 拒绝

**T5A/T5B（国际机 mail-node）**
- [x] `POST /internal/domains {t4-test.com}` → virtual_mailbox_domains 含测试域；返回 DNS 记录（T5A 已完成 Postfix 虚拟域，DKIM 记录当前为 pending）
- [x] `GET /internal/domains` 返回当前真实域 `example.com`
- [x] 该域下建邮箱 → vmailbox/users.conf 写入（T4B 验证账号 `t4b-auto-01@example.com` 已真实落国际机）
- [x] `DELETE /internal/domains/t4-test.com`（有邮箱）→ 拒绝；无邮箱 → 成功清理（本地 vmailbox_file 测试覆盖有邮箱拒绝，国际机无邮箱测试域已清理）
- [x] T5B：`/etc/opendkim/keys/<domain>/` 生成；SigningTable/KeyTable 各加一行；opendkim reload 后发信带 DKIM 签名（国际机已用 `t5b-check.example.com` 验证，随后已清理）

**T5C（移除与回归）**
- [x] 服务器页 → 国际机「域名池」→ 新域 → DNS 记录可展示/复制
- [x] 移除域名（该域有邮箱）→ 拒绝（国际机 DELETE `example.com` 已验证返回 `domain has mailbox accounts`）
- [x] 移除无邮箱测试域 → 远端 RemoveDomain 成功，本地 `server_domains.status=inactive`
- [x] DNS UI 按“原始域名 + A 记录主机头”生成最终记录；`t5-ui-a.example.com` 验证返回 A/MX/SPF/DKIM/DMARC 后已清理

**构建/回归**
- [x] mail-node `GOOS=linux GOARCH=amd64 go build -buildvcs=false` 通过，并已部署国际机
- [x] mgmt `go test ./...`、`go build -buildvcs=false ./cmd/server` 通过；AutoMigrate 建 server_domains 表；现有 example.com 邮箱/台账无回归
- [x] T5B 本地验证：mail-node `go test ./...`、mgmt `go test ./...`、mail-node linux/amd64 构建 `mail-node.t5b` 通过（2026-06-24）
- [x] T5B 国际机回归：`mail-node/postfix/opendkim` 均 active；`GET /internal/domains` 清理后仅返回 `example.com`；测试域 OpenDKIM 表项已移除
- [x] T5C 本地/国际机回归：RemoveDomain 有邮箱拒绝、无邮箱删除、重复删除 DKIM 残留清理均有测试或真实验证；测试域 `t5c-check.example.com` 已清理，最终仅剩 `example.com`

---

## 8. 与既有规划的关系
- 本文是 **T4/T5 的详细设计事实来源**。后续进入 T4A/T4B/T5A/T5B/T5C 时，以本文的模型、接口、状态语义和验证清单为准。
- `docs/design/phase3-mgmt-completion-plan.md` 只保留 Phase 3 总览、优先级与续接顺序；其中 T4/T5 章节不再展开细节，而是链接到本文。
- `context.md` 只记录当前进度、验证结果和下一步入口；需要看 T4/T5 的完整方案时，直接跳转本文。
- 本设计**取代**旧 `t4-domain-management.md`（全局域名 CRUD 方向，已删除）—— 那版把 T4 当独立全局 CRUD，偏离了「服务器域名池 + 服务器内建邮箱」的宝塔式诉求。
- 原 phase3 plan 的 T4（域名 CRUD）+ T5（server_domains + 域名感知分配）在本设计合并为一项交付。
- 鉴权（T6）、健康检查（T7）仍各自独立，不在本范围。
