# MailHub 控制面部署指南

> 最后校准：2026-07-11 | 适用版本：O2-P5 及以后

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
- 替换两个外部 API Token 占位值。
- 按需调整过滤和保留策略。
- 不要在该文件填写管理员用户名或密码。

`secrets/admin_password` 只写初始管理员密码。文件末尾换行会由 CLI 安全去除。

### 2.2 校验并启动

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

### 2.3 Docker 密码恢复

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

## 4. 旧版本升级到 O2-P5

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

先上传到临时路径并核对 SHA256，再用 `install` / 目录 swap 原子替换。回滚旧二进制不会删除 `admin_users` 或 `system_state`；但旧二进制会重新读取旧配置密码，因此回滚窗口内必须保留旧字段并限制配置文件权限。

## 7. 国际机 O2-P5 发布记录

2026-07-11 已在 `141.11.2.143` 完成 systemd 裸机升级：

- Git commit：`d8faef79705ba95f367e64486a812b3722b97c40`。
- Linux/amd64 binary SHA256：`8ac3894ba2e27084104fce46291e95703574d8827673a0e8ab0f1c4eda8be5d0`。
- 备份：`/opt/mgmt-system/backups/p5-20260711-064009`。
- 旧配置账号通过 `bootstrap-from-config` 一次性迁移，强制首次改密。
- `mgmt-system=active`，`/health`、`/health/ready`、新登录页、登录 CSS 和新 SPA 资源均返回 200。
- 错误密码返回 401；bootstrap 重复执行不覆盖现有数据库凭据。
