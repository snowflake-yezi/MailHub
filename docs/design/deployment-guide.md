# Phase 1B 部署指南：DNS + 邮箱服务器

> 域名：`example.com` | 服务器 IP：`203.0.113.20` | 日期：2026-06-17

---

## 1. 前置信息

| 项目 | 值 |
|------|-----|
| 域名 | `example.com` |
| 域名注册商 | 阿里云 / 腾讯云 / Godaddy 等（按实际情况选） |
| 邮箱服务器 IP | `203.0.113.20` |
| 操作系统 | 待确认（CentOS 7+ 或 Ubuntu 20.04+） |
| 邮箱域名 | `mail.example.com`（推荐用子域名） |
| 集成邮箱 | `union@example.com` |

---

## 2. DNS 配置

### 2.1 登录 DNS 管理后台

找到你购买 `example.com` 的平台（阿里云/腾讯云/Cloudflare/GoDaddy 等），进入 DNS 解析管理页面。

不同平台的入口：
- 阿里云：云解析 DNS → 域名解析列表 → `example.com` → 解析设置
- 腾讯云：DNS 解析 DNSPod → 域名列表 → `example.com` → 记录管理
- Cloudflare：选择域名 → DNS → Records

### 2.2 添加 DNS 记录

添加以下 **3 条记录**：

| 类型 | 主机记录 | 记录值 | TTL | 说明 |
|------|---------|--------|-----|------|
| **A** | `mail` | `203.0.113.20` | 600 | 邮箱服务器的 IP，让 `mail.example.com` 指向服务器 |
| **MX** | `@` | `mail.example.com` | 600 | 所有发往 `@example.com` 的邮件路由到 `mail.example.com` |
| **A** | `mgmt` | `203.0.113.20` | 600 | (可选) 管理平台的域名 `mgmt.example.com` |

> **MX 记录说明**：主机记录填 `@` 表示主域名 `example.com`，优先级填 `10`（或默认）。

配置完成后效果：

```
example.com     MX    mail.example.com (优先级 10)
mail.example.com  A   203.0.113.20
mgmt.example.com  A   203.0.113.20   ← 可选，管理后台用
```

### 2.3 验证 DNS 生效

等 1-10 分钟后在命令行执行：

```bash
# 验证 A 记录
nslookup mail.example.com
# 应该返回: 203.0.113.20

# 验证 MX 记录
nslookup -type=MX example.com
# 应该返回: mail.example.com

# 或在线验证
# https://toolbox.googleapps.com/apps/dig/#MX/example.com
# https://mxtoolbox.com/SuperTool.aspx
```

---

## 3. 云服务器初始化

### 3.1 SSH 登录

```bash
ssh root@203.0.113.20
```

### 3.2 安全检查清单

```bash
# 确认操作系统
cat /etc/os-release

# 确认内存/磁盘
free -h
df -h

# 检查端口 25 是否被云厂商封禁
# 阿里云、腾讯云默认封禁 25 端口，需要在控制台申请解封
# 如果 25 端口被禁，联系云厂商工单申请解封
```

### 3.3 配置防火墙

**如果用的 firewalld（CentOS 7+）：**
```bash
firewall-cmd --add-port=25/tcp --permanent      # SMTP 收信
firewall-cmd --add-port=8081/tcp --permanent    # mail-node API
firewall-cmd --reload
firewall-cmd --list-ports
```

**如果用的 ufw（Ubuntu）：**
```bash
ufw allow 25/tcp
ufw allow 8081/tcp
ufw status
```

**云厂商安全组也要放行**（在云控制台的安全组/防火墙规则里添加）：

| 端口 | 协议 | 来源 | 说明 |
|------|------|------|------|
| 25 | TCP | 0.0.0.0/0 | SMTP 收信（如需对外收信必须开） |
| 8081 | TCP | 管理平台 IP | mail-node 对内 API |
| 22 | TCP | 你的 IP | SSH 远程管理 |

---

## 4. 域名更新到管理系统

### 4.1 更新 config.yaml

`mgmt-system/config.yaml` 已经更新为：
```yaml
domains:
  - name: "example.com"
```

重启 mgmt-system 让域名生效。

### 4.2 在管理后台注册邮箱服务器

1. 打开 `http://localhost:8080/admin/servers`
2. 点击「注册服务器」
3. 填写：

```
名称:     mail-node-01
API地址:   203.0.113.20:8081
SMTP地址:  203.0.113.20
IMAP地址:  203.0.113.20
容量:      5000
```

---

## 5. 邮箱服务器部署（建议另起新会话）

以下为 Phase 1B 的完整步骤，建议在新的会话中逐项执行。

### 5.1 安装 Postfix + Dovecot

```bash
# CentOS 7+
yum install -y postfix dovecot
systemctl enable postfix dovecot

# Ubuntu 20.04+
apt-get install -y postfix dovecot-core dovecot-imapd
```

### 5.2 配置 Postfix

```bash
# /etc/postfix/main.cf
myhostname = mail.example.com
mydomain = example.com
virtual_mailbox_domains = example.com
virtual_mailbox_base = /var/mail/vhosts
virtual_mailbox_maps = hash:/etc/postfix/vmailbox
virtual_alias_maps = hash:/etc/postfix/virtual

# 禁止开放中继
smtpd_relay_restrictions = permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination
mynetworks = 127.0.0.1, 203.0.113.20/32
```

### 5.3 配置 Dovecot

```bash
# /etc/dovecot/dovecot.conf
mail_location = maildir:/var/mail/vhosts/%d/%n

passdb {
    driver = passwd-file
    args = /etc/dovecot/users.conf
}
userdb {
    driver = static
    args = uid=vmail gid=vmail home=/var/mail/vhosts/%d/%n
}
```

### 5.4 创建 vmail 用户

```bash
groupadd -g 5000 vmail
useradd -g vmail -u 5000 vmail -d /var/mail/vhosts -m
mkdir -p /var/mail/vhosts
chown -R vmail:vmail /var/mail/vhosts
```

### 5.5 编译并上传 mail-node

```bash
# 在本机编译 Linux 版本
cd D:\code\email_system\mail-node
set GOOS=linux
set GOARCH=amd64
go build -o mail-node cmd/node/main.go

# 上传到服务器
scp mail-node root@203.0.113.20:/usr/local/bin/
scp config.yaml root@203.0.113.20:/etc/mail-node/config.yaml
```

### 5.6 配置 mail-node systemd 服务

```bash
# /etc/systemd/system/mail-node.service
[Unit]
Description=Mail Node Service
After=network.target postfix.service dovecot.service

[Service]
Type=simple
ExecStart=/usr/local/bin/mail-node -config /etc/mail-node/config.yaml
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable mail-node
systemctl start mail-node
systemctl status mail-node
```

### 5.7 验证

```bash
# mail-node 健康检查
curl http://127.0.0.1:8081/internal/health

# 管理平台心跳上报
curl -X POST http://mgmt.example.com:8080/api/v1/internal/servers/heartbeat \
  -H "Content-Type: application/json" \
  -d '{"server_id":1,"status":"healthy"}'
```

---

## 6. 端到端测试

```
① 管理后台创建邮箱 → airline-test@example.com
② 从外部邮箱（Gmail/QQ）发邮件到 airline-test@example.com
③ 邮件到达 203.0.113.20:25 → Postfix → 过滤引擎
④ Dovecot 存档
⑤ SMTP 转发到 union@example.com
⑥ 运营在 union 邮箱查看
```

---

## 7. 常见问题

| 问题 | 排查 |
|------|------|
| DNS 解析不到 | `nslookup mail.example.com`，确认 A 记录添加正确，等待 TTL 过期 |
| 端口 25 不通 | 云厂商默认封禁，需工单申请解封；或改用 587/465 |
| 邮件发不进来 | `tail -f /var/log/maillog` 看 Postfix 日志 |
| mail-node 连不上管理平台 | 确认安全组/防火墙放行 8081；确认 mgmt IP 可达 |
| Dovecot 权限错误 | `chown -R vmail:vmail /var/mail/vhosts` |

---

## 8. 版本记录

| 日期 | 变更 |
|------|------|
| 2026-06-17 | 初版：DNS 配置 + 服务器部署步骤 |
