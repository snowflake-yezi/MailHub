# MailHub 控制面部署指南

> 最后校准：2026-07-16 | 适用版本：当前 `main`

本文覆盖 mgmt-system 控制面的 Docker Compose、裸机/systemd 和旧版本升级。mail-node、Postfix、Dovecot 与 OpenDKIM 见[数据面部署指南](design/deployment-guide.md)。

## 1. 管理员凭据模型

O2-P5 起，管理员凭据只有一个运行期事实源：数据库 `admin_users.password_hash`。

- `config.yaml auth.admin_user/admin_pass` 不参与运行期登录。
- `mgmt-server serve` 不创建、覆盖或重置管理员。
- 新部署必须先执行一次 `admin bootstrap`。
- 忘记密码使用 `admin reset-password`。
- 改密和 reset 会递增 `credential_version`，旧 Session 立即失效。
- Docker 的 `.env` / secret 或 systemd 的 password-file 只是安装输入，不是长期登录事实源。

不要在公网环境使用 `admin123` 等固定弱密码。release 模式的普通 bootstrap 和 reset 要求至少 12 位，并拒绝常见弱密码。

## 2. Docker Compose 新部署

### 2.1 准备配置

```bash
cd deploy/docker
cp .env.example .env
cp secrets/admin_password.example secrets/admin_password
chmod 600 .env secrets/admin_password
```

编辑 `.env`：

- `MAILHUB_ADMIN_USERNAME`：默认 `admin`。
- `MAILHUB_DB_PASSWORD`：MariaDB 业务账号密码。
- `MAILHUB_DB_ROOT_PASSWORD`：MariaDB root 密码，必须与业务密码不同。
- `MAILHUB_SHARED_SECRET`：mgmt 与 mail-node 的内部共享密钥。
- `MAILHUB_HTTP_PORT`：宿主机监听端口，默认只绑定 `127.0.0.1`。

编辑 `mgmt-config.yaml`：

- 将 `domains` 改为真实邮件域名。
- 按需调整过滤和保留策略。
- 不要在该文件填写管理员用户名或密码。
- 不要填写 `auth.tokens`；服务启动后在管理端“外部访问”页面创建应用并签发 Token。

`secrets/admin_password` 只写初始管理员密码。文件末尾换行会由 CLI 安全去除。

### 2.2 数据库与自动建表

Compose 中的 MariaDB 容器会创建 `email_mgmt` 数据库，并授予 `email_sys` 账号该库的建表和读写权限。`bootstrap` 或 `mgmt serve` 首次连接时，GORM AutoMigrate 会自动创建/更新当前控制面表。

新部署会创建 `api_applications`、`api_credentials`、`api_permissions`、`api_resources`、`api_application_permissions` 和 `api_access_logs`，不会创建历史明文表 `api_tokens`。AutoMigrate 只管理表结构，不会替非 Compose 部署创建 DSN 中指定的数据库。

### 2.3 校验并启动

```bash
docker compose config --quiet
docker compose build
docker compose up -d
docker compose ps
```

服务顺序：

```text
db healthy
  → bootstrap 一次性完成并退出 0
  → mgmt serve 启动
```

`bootstrap` 重复执行是幂等的：已初始化时返回成功，不覆盖数据库密码。

验证：

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8080/health/ready
docker compose logs --no-log-prefix bootstrap
docker compose logs --tail=100 mgmt
```

首次登录后，如果 Compose bootstrap 使用了 `--must-change-password`，后台会直接打开“管理账号设置”。

服务启动后登录管理端，在“外部访问”页面创建外部应用、勾选权限并签发一次性 Token。完整 Token 只显示一次，数据库只保存哈希。

### 2.4 Docker 密码恢复

将新密码写入 `secrets/admin_password` 后执行一次恢复容器：

```bash
docker compose run --rm bootstrap \
  admin reset-password \
  --username "${MAILHUB_ADMIN_USERNAME:-admin}" \
  --password-file /run/secrets/mailhub_admin_password \
  --must-change-password
```

恢复后可以换回或删除本地 secret 文件；不要把真实 `.env` 或 `secrets/admin_password` 提交到 Git。

## 3. 裸机/systemd 新部署

先创建数据库和业务账号。服务每次启动都会执行 AutoMigrate，因此运行账号必须持续拥有当前库的 DDL 和读写权限：

```sql
CREATE DATABASE email_mgmt CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'email_sys'@'127.0.0.1' IDENTIFIED BY '<强随机密码>';
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, DROP, REFERENCES
  ON email_mgmt.* TO 'email_sys'@'127.0.0.1';
FLUSH PRIVILEGES;
```

把该账号写入 `database.dsn`。新部署配置中不要添加 `auth.tokens`。

准备 root-only 密码文件：

```bash
install -d -m 0700 /etc/mgmt-system/secrets
install -m 0600 /dev/null /etc/mgmt-system/secrets/initial-admin-password
printf '%s' '<至少 12 位强密码>' > /etc/mgmt-system/secrets/initial-admin-password
```

初始化并启动：

```bash
cd /opt/mgmt-system
./mgmt-server admin bootstrap \
  --config /opt/mgmt-system/config.yaml \
  --username admin \
  --password-file /etc/mgmt-system/secrets/initial-admin-password \
  --must-change-password

systemctl start mgmt-system
```

确认登录成功后删除初始密码文件。之后修改 `config.yaml` 中的旧管理员字段不会改变登录密码。

## 4. 旧版本升级

升级前同时备份数据库、旧二进制和配置文件：

```bash
mysqldump --single-transaction --routines --triggers email_mgmt \
  > /var/backups/mailhub-email_mgmt-before-upgrade.sql
```

### 4.1 管理员凭据升级到 O2-P5

仅当旧版本仍使用 `config.yaml auth.admin_user/admin_pass` 且数据库尚未 bootstrap 时执行：

```bash
systemctl stop mgmt-system
cd /opt/mgmt-system
./mgmt-server admin bootstrap-from-config \
  --config /opt/mgmt-system/config.yaml
systemctl start mgmt-system
```

该迁移命令：

- 只在 `system_state.admin_bootstrap` 未完成时生效。
- 原样读取旧 YAML 凭据并写入 bcrypt hash。
- 强制 `must_change_password=true`。
- 重复执行不会覆盖数据库凭据。

迁移并完成首次改密后，应清空旧 `auth.admin_user/admin_pass`，避免运维人员误以为它们仍会覆盖数据库密码。

### 4.2 明文 API Token 退役

旧部署升级到当前版本时，`mgmt-server serve` 按以下顺序自动处理：

1. AutoMigrate 创建或更新当前哈希凭证表。
2. 同步当前 API 权限和资源。
3. 导入 `api_tokens` 中仍启用的 Token；同一次升级也会导入配置中的 `auth.tokens`。
4. 逐个确认 `api_credentials.token_hash` 唯一存在。
5. 验证全部成功后删除 `api_tokens`。

禁用的旧 Token 不会因配置中仍有同名明文项而复活。导入、验证或删表任一步失败，服务都会拒绝启动，旧表不会在验证失败时被删除。

启动后检查：

```sql
SHOW TABLES LIKE 'api_tokens';
SELECT COUNT(*) AS applications FROM api_applications;
SELECT COUNT(*) AS credentials FROM api_credentials;
```

第一条查询应返回空集。随后使用现有调用方 Token 验证外部 API，并在管理端确认应用、权限和凭证状态。验证完成后，从实际 `config.yaml` 删除整个 `auth.tokens` 配置段；以后只能从“外部访问”页面签发新 Token。

若旧表已经删除但配置中出现一个尚未导入的新 `auth.tokens`，服务会拒绝启动，不会把配置文件重新当作凭证签发入口。

## 5. 裸机密码恢复

```bash
systemctl stop mgmt-system
./mgmt-server admin reset-password \
  --config /opt/mgmt-system/config.yaml \
  --username admin \
  --password-file /etc/mgmt-system/secrets/recovery-admin-password \
  --must-change-password
systemctl start mgmt-system
```

## 6. 发布与回滚

每次裸机发布至少备份：

- `/opt/mgmt-system/mgmt-server`
- `/opt/mgmt-system/template/admin/login.html`
- `/opt/mgmt-system/template/static/login.css`
- `/opt/mgmt-system/template/static/login.js`
- `/opt/mgmt-system/template/static/admin-app/`

先上传到临时路径并核对 SHA256，再用 `install` / 目录 swap 原子替换。管理员迁移完成后，回滚旧二进制不会删除 `admin_users` 或 `system_state`；但旧二进制会重新读取旧配置密码，因此回滚窗口内必须保留旧字段并限制配置文件权限。

API Token 迁移的回滚边界更严格：`api_tokens` 删除后不得只替换回旧二进制。旧版本可能重新创建明文表，并从残留配置重新写入已撤销 Token。需要回滚时，必须停止服务，同时恢复升级前数据库备份、旧二进制和匹配的旧配置。
