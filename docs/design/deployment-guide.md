# 数据面部署指南：DNS + Postfix + Dovecot + OpenDKIM + mail-node

> 域名：`example.com` | 服务器 IP：`203.0.113.20` | 日期：2026-06-26
>
> **本文为新机部署的实操步骤，配置项占位值说明详见项目根目录 `DEPLOY.md`。**

---

## 1. 前置信息

| 项目 | 值 |
|------|-----|
| 域名 | `example.com` |
| 邮箱服务器 IP | `203.0.113.20` |
| 操作系统 | CentOS 7+ 或 Ubuntu 20.04+ |
| 主机名 | `mail.example.com` |
| 集成邮箱 | `union@example.com`（自建 Dovecot 账号） |
| vmail UID/GID | 5000:5000 |

---

## 2. DNS 配置

### 2.1 登录 DNS 管理后台

找到你购买 `example.com` 的平台（阿里云/腾讯云/Cloudflare/GoDaddy 等），进入 DNS 解析管理页面。

不同平台的入口：
- 阿里云：云解析 DNS → 域名解析列表 → `example.com` → 解析设置
- 腾讯云：DNS 解析 DNSPod → 域名列表 → `example.com` → 记录管理
- Cloudflare：选择域名 → DNS → Records

### 2.2 添加 DNS 记录

添加以下 **5 条记录**（mgmt 域名池页面添加域名后会自动生成此清单）：

| 类型 | 主机记录 | 记录值 | 说明 |
|------|---------|--------|------|
| **A** | `mail` | `203.0.113.20` | 收信服务器地址 |
| **MX** | `@` | `mail.example.com` | 优先级 10，邮件路由 |
| **TXT** | `@` | `v=spf1 a mx ~all` | SPF 反垃圾 |
| **TXT** | `mail._domainkey` | `v=DKIM1; k=rsa; p=...` | DKIM 公钥（mgmt 生成） |
| **TXT** | `_dmarc` | `v=DMARC1; p=quarantine` | DMARC 策略 |

> ⚠️ **PTR 反向解析**：部分云商（如荷兰 VH-GLOBAL）不支持配置 PTR。无 PTR 会导致 Gmail 进垃圾箱。新机选型优先选择支持 PTR 的云商。

### 2.3 验证 DNS 生效

```bash
nslookup mail.example.com          # A 记录
nslookup -type=MX example.com      # MX 记录
nslookup -type=TXT example.com     # SPF
nslookup -type=TXT mail._domainkey.example.com  # DKIM
nslookup -type=TXT _dmarc.example.com           # DMARC
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

```bash
# firewalld（CentOS 7+）
firewall-cmd --add-port=25/tcp --permanent     # SMTP 收信
firewall-cmd --add-port=587/tcp --permanent    # Submission（发信）
firewall-cmd --add-port=443/tcp --permanent    # HTTPS（Nginx）
firewall-cmd --add-port=8081/tcp --permanent   # mail-node API
firewall-cmd --reload
```

**云厂商安全组也要放行：**

| 端口 | 协议 | 来源 | 说明 |
|------|------|------|------|
| 25 | TCP | 0.0.0.0/0 | SMTP 收信 |
| 587 | TCP | 0.0.0.0/0 | Submission 发信 |
| 443 | TCP | 0.0.0.0/0 | HTTPS（管理后台 + Roundcube） |
| 8081 | TCP | 控制面 IP | mail-node 内部 API |
| 22 | TCP | 你的 IP | SSH |

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

## 5. 邮箱服务器部署

### 5.1 安装组件

```bash
# CentOS 7
yum install -y postfix dovecot
yum install -y epel-release
yum install -y opendkim
yum install -y python3                        # Python 2 中文编码有 bug

# Ubuntu 20.04+
apt-get install -y postfix dovecot-core dovecot-imapd opendkim opendkim-tools
```

### 5.2 配置 Postfix

```ini
# /etc/postfix/main.cf（关键项）
myhostname = mail.example.com
mydomain = example.com
inet_interfaces = all                          # 收外部信必须！
virtual_mailbox_domains = example.com
virtual_mailbox_base = /var/mail/vhosts
virtual_mailbox_maps = hash:/etc/postfix/vmailbox
virtual_uid_maps = static:5000
virtual_gid_maps = static:5000
virtual_alias_maps =                           # 清空

# SASL 认证（走 Dovecot）
smtpd_sasl_type = dovecot
smtpd_sasl_path = private/auth
smtpd_sasl_auth_enable = yes

# DKIM milter（连 OpenDKIM）
smtpd_milters = inet:127.0.0.1:8891
non_smtpd_milters = inet:127.0.0.1:8891

# 开放中继防护
smtpd_relay_restrictions = permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination
mynetworks = 127.0.0.1, 203.0.113.20/32

message_size_limit = 52428800                  # 50MB
```

```bash
# /etc/postfix/master.cf — 启用 submission（587）
# 取消 submission 行的注释，添加以下 -o 参数：
# submission inet n - n - - smtpd
#   -o syslog_name=postfix/submission
#   -o smtpd_tls_security_level=may
#   -o smtpd_tls_auth_only=no                 # Roundcube 兼容
#   -o smtpd_sasl_auth_enable=yes
```

> ⚠️ **CentOS 7 Postfix 2.10.1** 不支持 `postconf -P`，submission 参数必须直接编辑 `master.cf`。

### 5.3 配置 Dovecot

```
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

service auth {
    unix_listener /var/spool/postfix/private/auth {
        mode = 0660
        user = postfix
        group = postfix
    }
}
```

> ⚠️ **users.conf 必须是 `root:dovecot 640`**，不是 `vmail:vmail`！否则 Dovecot auth 进程（uid=97）无法读取，SASL 认证全部失败。

### 5.4 配置 OpenDKIM

```bash
# /etc/opendkim.conf
Mode    sv
Socket  inet:8891@localhost

# /etc/opendkim/SigningTable — mgmt 添加域名时自动写入
# /etc/opendkim/KeyTable     — mgmt 添加域名时自动写入
# /etc/opendkim/keys/        — DKIM 密钥存储目录

systemctl enable opendkim
systemctl start opendkim
```

> DKIM key 生成、表项写入、opendkim reload 均由 mail-node 在 mgmt 添加域名时自动执行，无需手动操作。

### 5.5 创建 vmail 用户

```bash
groupadd -g 5000 vmail
useradd -g vmail -u 5000 vmail -d /var/mail/vhosts -s /sbin/nologin
mkdir -p /var/mail/vhosts
chown -R vmail:vmail /var/mail/vhosts

# ⚠️ 必须验证！
id vmail
# 预期：uid=5000(vmail) gid=5000(vmail) groups=5000(vmail)
# 如果 UID 不是 5000，说明 useradd 静默失败（已有同名用户），必须先清理再重建
```

### 5.6 创建初始文件

```bash
touch /etc/dovecot/users.conf
chmod 640 /etc/dovecot/users.conf
chown root:dovecot /etc/dovecot/users.conf

touch /etc/postfix/vmailbox
postmap /etc/postfix/vmailbox
```

### 5.7 编译并上传 mail-node

```bash
# 交叉编译 Linux 版本（在开发机执行）
cd mail-node
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o mail-node ./cmd/node

# 上传
scp mail-node root@203.0.113.20:/usr/local/bin/
scp config.yaml root@203.0.113.20:/etc/mail-node/config.yaml
```

### 5.8 配置 systemd 并启动

```bash
# /etc/systemd/system/mail-node.service
[Unit]
Description=Mail Node Service
After=network.target postfix.service dovecot.service opendkim.service

[Service]
Type=simple
ExecStart=/usr/local/bin/mail-node
Environment=CONFIG_PATH=/etc/mail-node/config.yaml
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable postfix dovecot opendkim mail-node
systemctl restart postfix dovecot opendkim mail-node
systemctl status postfix dovecot opendkim mail-node
```

### 5.9 验证

```bash
# mail-node 健康检查
curl http://127.0.0.1:8081/internal/health
# → {"code":0,"data":{"status":"ok","total_messages":0}}

# 检查 Postfix 收信
echo "test" | mail -s "test" test@example.com
```

---

## 6. 端到端测试

```
① mgmt 后台注册 mail-node → 添加域名 → 记录 DNS 清单
② 在 DNS 控制台配置 A/MX/SPF/DKIM/DMARC
③ mgmt 在域名池下创建测试邮箱
④ 从外部邮箱（QQ/Gmail）发邮件到测试邮箱
⑤ 检查：mail-node API 可查询到邮件
⑥ 检查：union 邮箱收到转发（Subject 带 [源邮箱: xxx] 前缀）
⑦ 从 Roundcube union 邮箱发信回复，验证双向收发
```

---

## 7. 部署踩坑汇总

| 坑 | 现象 | 修复 |
|----|------|------|
| users.conf 权限为 `vmail:vmail 640` | Dovecot SASL 全部失败 | 必须 `root:dovecot 640` |
| `useradd -u 5000 vmail` 重名 | 静默失败，UID 非预期 | 部署后 `id vmail` 验证 |
| Postfix 2.10.1 无 `postconf -P` | 无法动态编辑 master.cf | 直接 `vim /etc/postfix/master.cf` |
| `inet_interfaces = localhost` | 收不到外部邮件 | 改为 `all` |
| 未装 Python 3 | 发中文邮件乱码 | `yum install python3` |
| OpenDKIM 不在默认源 | `yum install opendkim` 404 | `yum install epel-release` 先 |
| MariaDB 5.5 utf8mb4 索引限制 | AutoMigrate Error 1071 | 升 MariaDB 10.5 |
| htmx CDN 不可达 | 后台页面加载慢/报错 | 已本地化到 `/static/htmx.min.js` |

---

## 8. 版本记录

| 日期 | 变更 |
|------|------|
| 2026-06-17 | 初版：DNS 配置 + 服务器部署步骤 |
| 2026-06-26 | 更新至当前架构：OpenDKIM、DNS 五件套、Postfix 2.10 兼容、踩坑汇总 |
