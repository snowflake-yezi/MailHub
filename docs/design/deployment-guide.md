# 数据面部署指南：DNS + Postfix + Dovecot + OpenDKIM + mail-node

> 域名：`example.com` | 服务器 IP：`203.0.113.20` | 日期：2026-06-26
>
> **本文为新机部署的实操步骤，配置项占位值说明详见项目根目录 `DEPLOY.md`。**
>
> 实在不行直接让ai来帮忙部署

---

## 1. 前置信息(以下信息用自己的真实信息)

| 项目 | 值 |
|------|-----|
| 域名 | `example.com` |
| 邮箱服务器 IP | `203.0.113.20` |
| 操作系统 | CentOS 7+ / Rocky Linux 8+ / Debian 11+ / Ubuntu 20.04+ |
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

### 3.3 安装基础工具

```bash
# RHEL 系（CentOS / Rocky / AlmaLinux）
yum install -y curl wget tar unzip vim git openssl ca-certificates

# Debian 系（Debian / Ubuntu）
apt-get update
apt-get install -y curl wget tar unzip vim git openssl ca-certificates lsb-release
```

### 3.4 配置防火墙

```bash
# firewalld（CentOS / Rocky / AlmaLinux）
firewall-cmd --add-port=25/tcp --permanent     # SMTP 收信
firewall-cmd --add-port=587/tcp --permanent    # Submission（发信）
firewall-cmd --add-port=443/tcp --permanent    # HTTPS（Nginx）
firewall-cmd --add-port=8081/tcp --permanent   # mail-node API
firewall-cmd --reload

# ufw（Debian / Ubuntu，可选）
ufw allow 25/tcp
ufw allow 587/tcp
ufw allow 443/tcp
ufw allow from <控制面IP> to any port 8081 proto tcp
ufw allow from <你的IP> to any port 22 proto tcp
ufw reload
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
# RHEL 系（CentOS / Rocky / AlmaLinux）
yum install -y epel-release
yum install -y postfix dovecot opendkim python3

# Debian 系（Debian / Ubuntu）
DEBIAN_FRONTEND=noninteractive apt-get install -y postfix dovecot-core dovecot-imapd dovecot-pop3d opendkim opendkim-tools python3
```

> Debian / Ubuntu 安装 Postfix 时如果弹出交互配置，选择 `Internet Site`，系统邮件名填 `example.com`；后续仍以本文 `main.cf` 为准覆盖关键配置。

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

## 6. Roundcube Webmail 安装

Roundcube 是可选 Webmail 前端，推荐和集成邮箱所在的数据面同机部署。它通过 IMAP 读取 Dovecot，通过 SMTP submission 发送邮件。

### 6.1 安装 PHP / Nginx / 数据库依赖

```bash
# RHEL 系（CentOS / Rocky / AlmaLinux）
yum install -y nginx php php-fpm php-cli php-mbstring php-intl php-xml php-json php-pdo php-gd php-zip php-pear sqlite

# Debian 系（Debian / Ubuntu）
apt-get install -y nginx php-fpm php-cli php-mbstring php-intl php-xml php-json php-sqlite3 php-gd php-zip sqlite3
```

> PHP 包名会随发行版版本略有差异；如果缺少扩展，按 Roundcube installer 页面提示补装即可。

### 6.2 下载并初始化 Roundcube

```bash
cd /tmp
wget https://github.com/roundcube/roundcubemail/releases/download/1.6.9/roundcubemail-1.6.9-complete.tar.gz
tar -xzf roundcubemail-1.6.9-complete.tar.gz
mkdir -p /var/www
mv roundcubemail-1.6.9 /var/www/roundcube
cd /var/www/roundcube

# SQLite 最简单，适合单机 Webmail
mkdir -p /var/lib/roundcube
bin/initdb.sh --dir=SQL sqlite > /var/lib/roundcube/roundcube.sqlite.sql
sqlite3 /var/lib/roundcube/roundcube.sqlite < /var/lib/roundcube/roundcube.sqlite.sql
```

### 6.3 配置 Roundcube

创建 `/var/www/roundcube/config/config.inc.php`：

```php
<?php
$config['db_dsnw'] = 'sqlite:////var/lib/roundcube/roundcube.sqlite';
$config['imap_host'] = 'tls://localhost:993';
$config['smtp_host'] = 'tls://localhost:587';
$config['smtp_user'] = '%u';
$config['smtp_pass'] = '%p';
$config['support_url'] = '';
$config['product_name'] = 'MailHub Webmail';
$config['des_key'] = 'replace_with_24_random_chars';
$config['plugins'] = ['archive', 'zipdownload'];
$config['smtp_conn_options'] = [
    'ssl' => [
        'verify_peer' => false,
        'verify_peer_name' => false,
        'allow_self_signed' => true,
    ],
];
$config['imap_conn_options'] = $config['smtp_conn_options'];
```

生成 `des_key` 示例：

```bash
openssl rand -base64 18
```

设置权限：

```bash
# RHEL 系常见 PHP-FPM 用户是 apache；Debian/Ubuntu 常见是 www-data
chown -R apache:apache /var/www/roundcube /var/lib/roundcube 2>/dev/null || true
chown -R www-data:www-data /var/www/roundcube /var/lib/roundcube 2>/dev/null || true
```

### 6.4 Nginx 站点配置

如果 Roundcube 与管理后台共用一个域名，可把 `/admin`、`/api` 代理到 mgmt-system，根路径 `/` 给 Roundcube：

```nginx
server {
    listen 80;
    server_name mail.example.com;

    location /admin  { proxy_pass http://127.0.0.1:8080; }
    location /api    { proxy_pass http://127.0.0.1:8080; }
    location /static { proxy_pass http://127.0.0.1:8080; }

    root /var/www/roundcube;
    index index.php index.html;

    location / {
        try_files $uri $uri/ /index.php;
    }

    location ~ \.php$ {
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_pass 127.0.0.1:9000;     # RHEL 系常见
        # fastcgi_pass unix:/run/php/php-fpm.sock;  # Debian/Ubuntu 按实际 php-fpm sock 调整
    }
}
```

```bash
nginx -t && systemctl enable nginx && systemctl reload nginx
systemctl enable php-fpm 2>/dev/null || systemctl enable php*-fpm
systemctl restart php-fpm 2>/dev/null || systemctl restart php*-fpm
```

### 6.5 在 Roundcube 查看邮件

1. 打开 `https://mail.example.com/` 或你的 Webmail 域名。
2. 使用 Dovecot 邮箱账号登录，例如 `user@example.com` 和该邮箱密码。
3. 在「收件箱」查看真实投递到该邮箱的邮件。
4. 如果使用集成邮箱作为汇总收件箱，登录集成邮箱即可查看所有转发后的汇总邮件。
5. 发信测试：点击「撰写」，给外部邮箱或本域邮箱发信；若报 SMTP 认证失败，重点检查 Postfix `submission` 是否启用 `smtpd_sasl_auth_enable=yes` 且 `smtpd_tls_auth_only=no`。

---

## 7. Windows 本地开发 / 演示部署

Windows 适合运行控制面、前端和单元测试；不建议直接在 Windows 上承载生产 Postfix/Dovecot。需要完整邮件收发时，建议用 WSL2 或 Linux 虚拟机部署数据面。

### 7.1 准备依赖

- Go 1.22+
- Node.js 20+
- MySQL 8.0 / MariaDB 10.5+
- Git for Windows

### 7.2 启动控制面

```powershell
cd mgmt-system
copy config.example.yaml config.yaml
# 编辑 config.yaml：填写 DSN、admin、shared_secret、api_tokens

go run ./cmd/server
```

访问：

```text
http://localhost:8080/admin/
```

### 7.3 启动 React 管理后台开发服务

```powershell
cd mgmt-system/web
npm install
npm run dev
```

开发时可使用 Vite dev server；生产发布前运行：

```powershell
npm run build
```

### 7.4 Windows 上运行 mail-node 的限制

```powershell
cd mail-node
copy config.example.yaml config.yaml
go run ./cmd/node
```

这只能验证 HTTP API、配置加载和部分业务逻辑。真实收发邮件依赖 Postfix/Dovecot/OpenDKIM，建议在 Linux/WSL2 中部署数据面后，再把 Windows 上的 mgmt-system 指向该数据面 API。

### 7.5 Windows 本地测试

```powershell
go test ./mgmt-system/...
go test ./mail-node/...
npm --prefix mgmt-system/web run build
```

---

## 8. 在管理后台查看邮件

1. 登录 `http://<控制面地址>:8080/admin/`。
2. 进入「服务器」页面，确认数据面节点状态为 healthy。
3. 进入「服务器 → 域名池」，添加域名并按页面提示配置 DNS。
4. 在域名池下创建邮箱，或通过邮箱页面批量创建邮箱。
5. 进入「邮件」页面，输入目标邮箱地址并查询。
6. 打开邮件详情：
   - 「文本」查看纯文本正文；
   - 「HTML 预览」查看经过安全处理的 HTML 正文；
   - 「附件」下载附件或 inline 图片。
7. inline 图片场景下，系统会把 HTML 中的 `cid:` 引用映射到附件下载接口，并按图片魔数修复缺失的后缀和 Content-Type。

---

## 9. 端到端测试

```
① mgmt 后台注册 mail-node → 添加域名 → 记录 DNS 清单
② 在 DNS 控制台配置 A/MX/SPF/DKIM/DMARC
③ mgmt 在域名池下创建测试邮箱
④ 从外部邮箱发邮件到测试邮箱
⑤ 在管理后台「邮件」页面查询该邮箱，确认正文、附件、inline 图片正常
⑥ 登录 Roundcube 集成邮箱，确认收到转发汇总邮件
⑦ 从 Roundcube 集成邮箱发信回复，验证双向收发
```

---

## 10. TLS 证书（Let's Encrypt）

### 10.1 安装 certbot

```bash
# CentOS 7
yum install -y epel-release
yum install -y certbot

# Ubuntu 20.04+
apt-get update
apt-get install -y certbot
```

### 10.2 申请证书

使用 webroot 方式（Nginx 需已监听 80 端口做验证）：

```bash
# 先确保 Nginx 有 80 端口的 server 块
certbot certonly --webroot -w /var/www/html -d mail.example.com
```

证书生成后位于：
- 证书：`/etc/letsencrypt/live/mail.example.com/fullchain.pem`
- 私钥：`/etc/letsencrypt/live/mail.example.com/privkey.pem`

### 10.3 Nginx 配置 TLS

```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate     /etc/letsencrypt/live/mail.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.example.com/privkey.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # mgmt-system 后台 + API
    location /admin  { proxy_pass http://127.0.0.1:8080; }
    location /api    { proxy_pass http://127.0.0.1:8080; }
    location /static { proxy_pass http://127.0.0.1:8080; }

    # Roundcube（同机部署时）
    location / {
        root /var/www/roundcube;
        index index.php;
    }
    location ~ \.php$ {
        fastcgi_pass 127.0.0.1:9000;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        include fastcgi_params;
    }
}

# HTTP → HTTPS 重定向
server {
    listen 80;
    server_name mail.example.com;
    return 301 https://$host$request_uri;
}
```

```bash
nginx -t && systemctl reload nginx
```

### 10.4 Postfix TLS

```bash
# 编辑 /etc/postfix/main.cf
postconf -e 'smtpd_tls_cert_file = /etc/letsencrypt/live/mail.example.com/fullchain.pem'
postconf -e 'smtpd_tls_key_file = /etc/letsencrypt/live/mail.example.com/privkey.pem'
postconf -e 'smtpd_tls_security_level = may'
postconf -e 'smtpd_tls_loglevel = 1'

# submission 也要证书（master.cf 中 -o 参数或用 postconf -P，Postfix 2.11+）
# Postfix 2.10.1：在 /etc/postfix/master.cf 的 submission 行下确保已配：
#   -o smtpd_tls_security_level=may
#   -o smtpd_tls_auth_only=no

postfix reload
```

验证 STARTTLS：
```bash
openssl s_client -connect mail.example.com:25 -starttls smtp </dev/null 2>/dev/null | grep -E 'subject=|issuer='
# 预期看到 Let's Encrypt 签发的证书信息
```

### 10.5 Dovecot SSL

```bash
# /etc/dovecot/conf.d/10-ssl.conf
ssl = required
ssl_cert = </etc/letsencrypt/live/mail.example.com/fullchain.pem
ssl_key = </etc/letsencrypt/live/mail.example.com/privkey.pem
ssl_protocols = !SSLv2 !SSLv3 !TLSv1 !TLSv1.1
```

```bash
dovecot reload
```

验证 IMAP SSL：
```bash
openssl s_client -connect mail.example.com:993 </dev/null 2>/dev/null | grep -E 'subject=|issuer='
```

### 10.6 证书自动续期

Let's Encrypt 证书有效期 90 天，需定期续期：

```bash
# 添加 cron 任务（root 用户）
crontab -e

# 每天凌晨 3:27 检查续期，成功后 reload 相关服务
27 3 * * * certbot renew --quiet --post-hook "systemctl reload nginx postfix dovecot"
```

验证自动续期配置：
```bash
certbot renew --dry-run
```

### 10.7 证书文件权限

```bash
# Let's Encrypt 私钥仅 root 可读
chmod 640 /etc/letsencrypt/live/mail.example.com/privkey.pem
chmod 640 /etc/letsencrypt/archive/mail.example.com/privkey*.pem

# Nginx/Dovecot 以 root 启动（读私钥），worker 降权
# 如果 Nginx 以非 root 运行，需将 nginx 用户加入私钥组或使用 ACL
```

---

## 11. 部署踩坑汇总

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

## 12. 版本记录

| 日期 | 变更 |
|------|------|
| 2026-06-17 | 初版：DNS 配置 + 服务器部署步骤 |
| 2026-06-26 | 更新至当前架构：OpenDKIM、DNS 五件套、Postfix 2.10 兼容、踩坑汇总 |
| 2026-06-30 | T10 收尾：新增 §10 TLS 证书（Let's Encrypt 获取/部署/续期、Postfix/Dovecot/Nginx） |
| 2026-07-07 | 补充 Debian/Ubuntu/RHEL 通用安装、Windows 本地开发、Roundcube 安装与管理后台/Webmail 查看邮件步骤 |
