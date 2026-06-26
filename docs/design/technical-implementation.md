# 技术实现方案

> 版本: v1.0 | 日期: 2026-06-17 | 状态: 待评审

---

## 1. 概述

本文档描述国际订单邮箱系统的跨服务器技术实现：管理系统如何远程调用邮箱服务器、如何在目标服务器上创建邮箱账号、以及邮件的收发链路。

---

## 2. 数据分布

```
┌─ 管理系统 ────────────────────────────────────────┐
│  MySQL (元数据):                                   │
│  ├ domains         — 域名池                        │
│  ├ mail_servers    — 邮箱服务器池（IP、端口、容量）  │
│  ├ order_mailboxes — 邮箱账号 + 订单绑定关系        │
│  ├ filter_rules    — 过滤规则（逻辑主本）           │
│  └ api_tokens      — API 鉴权                     │
│                                                    │
│  不存邮件内容，不存附件                             │
└────────────────────────────────────────────────────┘

┌─ 邮箱服务器 × N ──────────────────────────────────┐
│  磁盘 (/var/mail/vhosts/):                         │
│  ├ {domain}/{user}/cur/    ← 邮件文件 (.eml)       │
│  ├ {domain}/{user}/new/    ← 新邮件暂存            │
│  └ {domain}/{user}/tmp/    ← 处理中               │
│                                                    │
│  配置文件:                                         │
│  ├ /etc/dovecot/users.conf  — 账号/密码            │
│  └ /etc/postfix/vmailbox    — virtual mailbox 映射  │
│                                                    │
│  Postfix (SMTP :25)    — 收信                      │
│  Dovecot (IMAP :143)   — 存信/取信                  │
│  mail-node (Go :8081)  — 过滤引擎 + 对内 API       │
└────────────────────────────────────────────────────┘
```

---

## 3. 跨服务器通信

### 3.1 通信模型

```
管理系统 (1 台) ──HTTP 内网──→ 邮箱服务器-01 (:8081)
                 ──HTTP 内网──→ 邮箱服务器-02 (:8081)
                 ──HTTP 内网──→ 邮箱服务器-N  (:8081)
```

### 3.2 短信协议

- **协议**: HTTP/1.1
- **格式**: JSON
- **内容格式**: `Content-Type: application/json`
- **鉴权**: 内部固定 Token（`X-Internal-Token` 请求头），不对外暴露
- **超时**: 10 秒 (创建账号、查询邮件)；3 秒 (健康检查)

### 3.3 管理系统 → 邮箱服务器 API

| 方法 | 路径 | 说明 | 权重 |
|------|------|------|------|
| POST | `/internal/mailboxes` | 创建邮箱账号 | 核心 |
| DELETE | `/internal/mailboxes/:email` | 删除/回收邮箱 | |
| GET | `/internal/mailboxes/:email/messages` | 获取邮件列表 | |
| GET | `/internal/messages/:id` | 获取单封邮件完整内容 | |
| GET | `/internal/health` | 健康检查 + 负载信息 | 核心 |
| GET | `/internal/filters` | 拉取最新过滤规则 | |
| POST | `/internal/filters/reload` | 强制重载规则 | |

### 3.4 内部 API 请求/响应格式

#### POST /internal/mailboxes — 创建邮箱

```
Request:
{
    "email_address": "airline-cz-001@mail.xxx.com",
    "password": "aB3xKp9m"
}

Response 201:
{
    "code": 0,
    "message": "created",
    "data": {
        "email_address": "airline-cz-001@mail.xxx.com",
        "domain": "mail.xxx.com",
        "local_part": "airline-cz-001",
        "maildir_path": "/var/mail/vhosts/mail.xxx.com/airline-cz-001"
    }
}
```

#### GET /internal/health — 健康检查

```
Response 200:
{
    "code": 0,
    "data": {
        "status": "ok",
        "load": 342,           // 当前邮箱数
        "capacity": 5000,
        "disk_usage": "23%",
        "uptime": 86400,
        "node_id": 1,
        "node_name": "mail-node-01"
    }
}
```

---

## 4. 邮箱账号创建流程（端到端）

### 4.1 单账号创建

```
运营在 Web 后台提交:
┌─────────────────────────────────┐
│  邮箱前缀: airline-cz-001       │
│  域名:     mail.xxx.com         │  (从域名池下拉)
│  密码:     (留空自动生成)        │
│  目标服务器: auto (负载最小的)    │
└─────────────────────────────────┘
          │
          ▼
Step 1 — 管理系统校验
  ├ 检查前缀 + 域名是否已存在于 order_mailboxes 表
  ├ 检查域名是否 active
  └ 选服务器: 如果"auto"，查 mail_servers WHERE status='healthy'
     ORDER BY current_load ASC LIMIT 1

Step 2 — 管理系统调用邮箱服务器
  POST http://{server.api_host}/internal/mailboxes
  Headers: X-Internal-Token: internal-proxy-token
  Body: {email_address, password}
  
  ├ 成功 → Step 3
  └ 失败 → 返回错误，不写入本地 DB

Step 3 — 邮箱服务器本地操作
  mail-node HandleCreateMailbox():
  ├ 创建 Maildir 目录结构
  │   /var/mail/vhosts/{domain}/{user}/
  │   ├ cur/      ← 已读邮件
  │   ├ new/      ← 未读/新邮件
  │   └ tmp/      ← 处理中
  ├ 设置权限: chown -R vmail:vmail {maildir}
  ├ 追加 Dovecot 用户: /etc/dovecot/users.conf
  │   {email}:{PLAIN}{password}::::::
  └ 追加 Postfix vmailbox: /etc/postfix/vmailbox
      {email} {domain}/{user}/
      → 执行 postmap 刷新 + postfix reload

Step 4 — 管理系统写入本地记录
  INSERT INTO order_mailboxes (
    email_address, local_part, domain_id, server_id,
    status='active', created_at=now()
  )
  UPDATE mail_servers SET current_load = current_load + 1

Step 5 — 返回结果
  {code: 0, data: {email_address, created_at}}
```

### 4.2 批量创建

```
运营上传 CSV 文件:
  airline-cz-001,
  airline-cz-002,
  airline-cz-003,mypass123

管理系统逐行处理:
  for each row in CSV:
    ├ password = row.password || autoGenerate()
    ├ 验重: 查询 order_mailboxes 是否已有
    ├ 分配: 按当前负载选服务器
    ├ 下发: HTTP POST → 邮箱服务器
    └ 记录: INSERT 到 order_mailboxes

  → 返回汇总:
    {total: 3, success: 3, failed: 0,
     results: [{prefix, email, server, status}, ...]}
```

### 4.3 密码处理

- 创建时不传密码 → 自动生成 16 位随机密码
- 密码明文写入 Dovecot `users.conf`（Phase 1 不加密，后续可改 `{SHA512-CRYPT}`）
- 密码仅存储于邮箱服务器本地，管理系统的 MySQL 不存密码

---

## 5. 邮件收发链路

### 5.1 收信（航司 → 邮箱服务器）

```
                  DNS 查 MX 记录
                  mail.xxx.com → 1.2.3.4
                          │
航司 SMTP ────→ mail.xxx.com:25 ────→ Postfix (smtpd)
                                         │
                                    before-queue
                                    content_filter
                                         │
                                         ▼
                              ┌────────────────────┐
                              │  mail-node 过滤引擎  │
                              │  (Go 程序 :8081)   │
                              │                    │
                              │  规则匹配:          │
                              │  ① 白名单发件人 → pass│
                              │  ② 黑名单发件人 → block│
                              │  ③ 关键词匹配 → 打分│
                              │  ④ 无匹配 → pass+flag│
                              └────────┬───────────┘
                                       │
                         ┌─────────────┼─────────────┐
                         ▼             ▼             ▼
                       PASS          FLAG          BLOCK
                         │             │              │
                         ▼             ▼              ▼
                  Dovecot 入库    Dovecot 入库   归档/DISCARD
                  Maildir/cur     Maildir/cur    (不转发)
                         │             │
                         └──────┬──────┘
                                ▼
                    ┌─────────────────────────┐
                    │  SMTP 转发到集成邮箱      │
                    │  目标: union@xxx.com     │
                    │                         │
                    │  PASS: 标题保持原样      │
                    │  FLAG: 标题加 [疑似] 前缀 │
                    │  标题格式:               │
                    │  [源: {mailbox_addr}]   │
                    │  {原始标题}              │
                    └─────────────────────────┘
                                │
                                ▼
                    union@xxx.com 收件箱
                    (运营统一查看)
```

### 5.2 Postfix content_filter 配置

```conf
# /etc/postfix/master.cf
# 定义过滤服务
filter    unix  -       n       n       -       10      pipe
  flags=Rq user=filter argv=/usr/local/bin/filter-postfix
  http://127.0.0.1:8081/smtp/filter

# /etc/postfix/main.cf  
content_filter = filter:127.0.0.1:10025
```

> `filter-postfix` 是一个轻量 shell 脚本（或 Go 编译的小工具），把 Postfix 传来的原始邮件内容 POST 到 mail-node 的 `/smtp/filter` 端点，等到过滤结果后决定放行/拒绝。

### 5.3 发信（可选，Phase 1 不做）

Phase 1 仅收信 + 转发。如需邮件服务器主动发信（如自动回复），在 Phase 2 扩展。

---

## 6. 过滤规则热更新

```
┌──────────────────────┐         每 30 秒
│  管理系统 (规则主本)  │ ──GET /internal/filters──→ ┌─────────────┐
│  filter_rules 表     │ ←────返回规则 JSON─────── │  mail-node  │
└──────────────────────┘                            │             │
                                                    │ 更新内存    │
                                                    │ sync.RWMutex│
                                                    │ 规则生效    │
                                                    └─────────────┘
```

或者规则变更后，管理系统主动 POST `/internal/filters/reload` 通知立即更新。

---

## 7. 管理系统 → 邮箱服务器故障处理

| 故障场景 | 处理策略 |
|----------|---------|
| 邮箱服务器不可达 (超时 10s) | 返回错误，不写入本地 DB；下次创建时自动选择其他健康服务器 |
| 邮箱服务器创建成功但本地 DB 写入失败 | 回滚：调用邮箱服务器 DELETE 删除刚创建的账号 |
| 邮箱服务器返回错误（前缀已存在等） | 透传错误给前端，不做本地记录 |
| 邮箱服务器心跳超时 N 次 | 管理系统标记该服务器状态为 `degraded`，暂停分配新邮箱 |
| 邮箱服务器宕机 | 状态自动变为 `down`，触发告警；已有邮件不受影响（发件方 SMTP 会重试） |

---

## 8. Phase 1 部署形态

```
                    ┌─────────────────────────────────────┐
                    │  云服务器 × 1 (建议 4C8G 100G)       │
                    │  IP: 1.2.3.4                        │
                    │  OS: Ubuntu 20.04+ / CentOS 7+      │
                    │                                     │
                    │  ┌─────────────────────────────┐    │
                    │  │  MySQL 8.0 (:3306)          │    │
                    │  │  元数据存储                   │    │
                    │  └─────────────────────────────┘    │
                    │                                     │
                    │  ┌─────────────────────────────┐    │
                    │  │  mgmt-system (Go, :8080)     │    │
                    │  │  Web 后台 + API              │    │
                    │  │  Nginx 反代 (:443) → :8080    │    │
                    │  └─────────────────────────────┘    │
                    │                                     │
                    │  ┌─────────────────────────────┐    │
                    │  │  mail-node (Go, :8081)       │    │
                    │  │  过滤引擎 + 对内 API          │    │
                    │  └─────────────────────────────┘    │
                    │                                     │
                    │  ┌─────────────────────────────┐    │
                    │  │  Postfix (:25)               │    │
                    │  │  Dovecot (:143)              │    │
                    │  │  Maildir: /var/mail/vhosts/  │    │
                    │  └─────────────────────────────┘    │
                    │                                     │
                    │  DNS: mail.xxx.com A+MX → 1.2.3.4  │
                    └─────────────────────────────────────┘

                    集成邮箱: union@xxx.com
                     (阿里企业邮 / 腾讯企业邮 / 自建 Dovecot+Roundcube)
```

Phase 2 扩容时只需新增云服务器，装上 Postfix + Dovecot + mail-node，在后台注册即可纳入管理。

---

## 9. 安全性

| 关注点 | 实现 |
|--------|------|
| 管理系统 → 邮箱服务器通信 | 内网 IP + `X-Internal-Token` 固定密钥（非公网暴露） |
| API 对外暴露 | `Authorization: Bearer <token>` + Token 存储在 DB 可轮换 |
| SMTP 开放中继防护 | `mynetworks = 127.0.0.1, 10.0.0.0/8`，其他全部 reject |
| SMTP TLS | Phase 2 启用（Let's Encrypt 证书） |
| 邮箱密码存储 | Phase 1 明文存 Dovecot users.conf（Phase 2 改 SHA512-CRYPT） |

---

## 10. 版本记录

| 日期 | 变更 | 作者 |
|------|------|------|
| 2026-06-17 | 初版 | Claude |
