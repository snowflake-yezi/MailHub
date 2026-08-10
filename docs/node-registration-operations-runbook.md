# Mail-node 节点注册与 dual 迁移生产运维手册

> 文档类型：当前生产运维 Runbook（NR-P7）
>
> 适用对象：执行节点接入、Legacy 原位迁移、canary、回滚和交接的运维人员
>
> 原理与异常恢复：[节点注册与加入集群指南](node-registration-guide.md)
>
> 设计边界：[NR-P7 Canary and Legacy Rollback](design/node-registration-p7-canary-rollback.md)

本文只使用占位符，不记录生产密码、Enrollment Token、request secret、节点 credential、数据库 DSN、Session Cookie 或完整配置文件。生产运行数据和回滚证据应保存在受控运维系统中。

---

## 1. 先分清四个概念

当前系统不是“双注册”。一台节点只完成一次身份注册，注册后获得一个永久 `node_uuid` 和可轮换的独立 credential。迁移期间同时存在的是两种鉴权路径和两条传输路径。

| 维度 | 可选值 | 含义 |
|---|---|---|
| 注册用途 | 新节点、Legacy 原位迁移、身份恢复 | 本次邀请要创建、迁移还是恢复哪个身份 |
| 身份约束 | 标准审批、预绑定 UUID | 邀请是否预先限制特定 `node_uuid` |
| 运行鉴权 | 节点独立 credential、shared secret | credential 用于新控制通道；shared secret 只在迁移期保留兼容 |
| transport | `legacy_http`、`dual`、`control_stream` | system 实际使用哪条路径调用 node |

`dual` 是 transport 模式，不是注册模式，也不是让节点注册两次：

- 命令使用 ControlStream。
- 读取优先使用 DataStream。
- DataStream 暂时不可用时允许回退 Legacy HTTP。
- node `8081` 和 shared secret 必须保留到 canary 与回滚验收结束。

---

## 2. 迁移状态机

每台现有生产节点必须独立按下表推进，不允许全量同时切换：

| 阶段 | enrollment | node 本地 transport | system server transport | 允许动作 |
|---|---|---|---|---|
| 0. Legacy | `legacy_approved` | `legacy_http` | `legacy_http` | 备份和建立回滚基线 |
| 1. 已注册 | `approved` | `legacy_http` | `legacy_http` | 核对 UUID、server ID 和 credential |
| 2. 已建连 | `approved` | `dual` | `legacy_http` | 核对 Control/Data、ready 和 lease |
| 3. Canary | `approved` | `dual` | `dual` | 全业务验证和回滚演练 |
| 4. 最终态 | `approved` | `control_stream` | `control_stream` | 全 fleet 验收后才关闭兼容入口 |

硬约束：

1. 注册完成不会自动切换 transport。
2. node 必须先建立 ControlStream、报告 `ready` 并持有有效 lease，system 才能进入 `dual` 或 `control_stream`。
3. `dual` 期间不能关闭 `node_control.legacy_http_enabled`。
4. 回滚 transport 不撤销节点身份、credential 或持久命令结果。
5. 撤销 credential 不等于回滚 transport；删除 server 更不等于取消注册。

---

## 3. 占位符和配置归属

执行前把下列占位符写入受控变更单，不要临时猜测：

| 占位符 | 示例格式 |
|---|---|
| `<management-domain>` | `mgmt.example.com` |
| `<management-host>` | 控制面 SSH 地址 |
| `<node-host>` | 当前节点 SSH 地址 |
| `<server-id>` | 原 `mail_servers.id` |
| `<server-name>` | 后台原节点名称 |
| `<node-source-cidr>` | 节点访问控制面的来源地址 |

两个 YAML 文件职责不同：

| 文件 | 所在机器 | 负责内容 |
|---|---|---|
| `/opt/mgmt-system/config.yaml` | 控制面 | 数据库、管理鉴权、`node_control` TLS listener |
| `/etc/mail-node/config.yaml` | 每台 node | Maildir、node ID、`management.*`、转发、DKIM、identity |

出现 `database`、`auth`、`domains`、`node_control` 的是控制面配置。出现 `maildir`、`management`、`forward`、`node`、`dkim` 的是节点配置。不要把两个文件合并。

---

## 4. 启用控制面 gateway

### 4.1 变更前检查

- 备份控制面配置、二进制、数据库和管理前端。
- 确认证书覆盖 `<management-domain>`，证书和私钥可由 `mgmt-system` 运行用户读取。
- 确认控制面和 node 时间同步。
- 防火墙只允许计划内 node 来源访问 TCP `8443`。
- 保留现有 HTTP `8080`、node `8081` 和 shared secret。

在 `/opt/mgmt-system/config.yaml` 中配置一个且仅一个 `node_control` 段：

```yaml
node_control:
  enabled: true
  listen: ":8443"
  public_url: "https://<management-domain>:8443"
  tls_cert_file: "/path/to/fullchain.pem"
  tls_key_file: "/path/to/privkey.pem"
  heartbeat_interval_seconds: 30
  lease_timeout_seconds: 90
  command_timeout_seconds: 15
  data_max_concurrency_per_node: 4
  data_chunk_size: 262144
  legacy_http_enabled: true
```

重启并验证：

```bash
systemctl restart mgmt-system
systemctl is-active mgmt-system
ss -lntp | grep -E ':(8080|8443)\b'
curl -fsS --max-time 3 http://127.0.0.1:8080/health/ready
```

本机 TLS 验证：

```bash
openssl s_client \
  -connect 127.0.0.1:8443 \
  -servername <management-domain> \
  -verify_return_error </dev/null 2>&1 |
grep -E 'subject=|issuer=|Verify return code'
```

每台 node 还必须独立验证公网路径；本机成功不代表安全组、NAT 和跨机路由成功：

```bash
openssl s_client \
  -connect <management-domain>:8443 \
  -servername <management-domain> \
  -verify_return_error </dev/null 2>&1 |
grep -E 'subject=|issuer=|Verify return code'
```

门禁：所有测试都返回成功、证书 `Verify return code: 0 (ok)` 后才能创建生产邀请。

---

## 5. Legacy 节点原位注册

### 5.1 创建邀请

在“服务器池 -> 创建注册邀请”中填写：

- 注册用途：迁移现有 Legacy 节点。
- 迁移目标：准确选择 `<server-id>` / `<server-name>`。
- 有效期：建议 15 分钟。
- 最大使用次数：1。
- 自动批准：关闭。

邀请页显示的完整 Enrollment Token 只出现一次。不得放入聊天、工单正文、截图、命令参数、文件名或 Shell 历史。

### 5.2 安全保存 Token

`--token-file` 接收文件路径，不接收 Token 本身。文件名始终固定为 `/run/mail-node/enrollment-token`。

```bash
install -d -m 0700 /run/mail-node
install -m 0600 /dev/null /run/mail-node/enrollment-token

read -r -s -p "Enrollment Token: " MAILHUB_ENROLL_TOKEN
printf '\n'
printf '%s\n' "$MAILHUB_ENROLL_TOKEN" > /run/mail-node/enrollment-token
unset MAILHUB_ENROLL_TOKEN

test -s /run/mail-node/enrollment-token && echo "token file ready"
wc -c < /run/mail-node/enrollment-token
```

只在 `Enrollment Token:` 提示后输入 Token。不要修改重定向目标。若 Token 出现在聊天、历史、文件名或日志中，立即撤销邀请、删除临时文件并创建新邀请，不能继续使用。

### 5.3 发起申请并审批

公开受信任证书不传 `--ca-file`：

```bash
mail-node enroll \
  --management-url https://<management-domain> \
  --token-file /run/mail-node/enrollment-token \
  --name <server-name>
```

CLI 输出 `pending` 后保持终端运行。审批人必须核对：

- Request ID 与 CLI 一致。
- Node UUID 与 CLI 一致。
- 迁移目标是原 `<server-id>`，不是新建 server。
- 主机名、机器指纹、来源地址和 Agent 版本符合资产记录。
- 原邮箱数、域名、容量、地址和分配状态未变化。

批准后 CLI 应输出 `completed` 和 credential 元数据，但不能输出或转发 credential 明文。

### 5.4 验证并清理

```bash
rm -f /run/mail-node/enrollment-token

mail-node identity show \
  --directory /var/lib/mail-node/identity \
  --credential-file /var/lib/mail-node/identity/credential
```

门禁：

- `credential_present=true`。
- 后台仍是原 `<server-id>`。
- `enrollment_state=approved`。
- system server transport 仍为 `legacy_http`。

---

## 6. 让 node 建立 dual 通道

先备份 `/etc/mail-node/config.yaml`，然后只修改已有 `management` 段；不得创建第二个同名段：

```yaml
management:
  api_url: "<保留当前值>"
  control_url: "<management-domain>:8443"
  transport_mode: "dual"
  credential_file: "/var/lib/mail-node/identity/credential"
  ca_file: ""
  heartbeat_interval: 30
  filter_sync_interval: 3600

identity:
  directory: "/var/lib/mail-node/identity"
```

`control_url` 必须是 `host:port`，不能包含 `https://`。公共证书的 `ca_file` 留空；私有 CA 才填写 PEM 路径。

重启并验证：

```bash
chmod 0600 /etc/mail-node/config.yaml
systemctl restart mail-node
sleep 3
systemctl is-active mail-node
ss -lntp | grep ':8081'

journalctl -u mail-node --since '-2 min' --no-pager |
grep -E '\[(control|data)\]|session ended|Failed|fatal|error'
```

成功日志至少包含：

```text
[control] connected session=... protocol=1
[data] connected session=... concurrency=... chunk=...
```

后台门禁：

- `enrollment_state=approved`。
- `connection_state=connected`。
- `readiness_state=ready`。
- `lease_expires_at` 在未来且持续刷新。
- `desired_revision=applied_revision`。
- system server transport 仍为 `legacy_http`。

---

## 7. 将 system server 切到 dual

当前管理端没有 transport 切换按钮。运维人员可在已登录管理后台的同源浏览器 Console 调用受 Session 保护的管理 API：

```javascript
fetch('/api/v1/admin/servers/<server-id>/transport', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({transport_mode: 'dual'})
}).then(r => r.json()).then(console.log)
```

必须确认返回：

```text
code: 0
data.transport_mode: "dual"
data.enrollment_state: "approved"
data.connection_state: "connected"
data.readiness_state: "ready"
```

浏览器若同时报告无关域名 Cookie 被拒绝，但管理 API 返回 `code: 0`，该 Cookie 警告不影响本次切换。不得从 Console 复制 Session Cookie。

---

## 8. Dual canary 验收

### 8.1 状态稳定性

- [ ] transport 保持 `dual`。
- [ ] ControlStream 和 DataStream 不反复重连。
- [ ] lease、最近心跳和最近探测持续刷新。
- [ ] `desired_revision=applied_revision`。
- [ ] `probe_fail_count=0`。
- [ ] Postfix、Dovecot、OpenDKIM 和 `mail-node` 均 active。
- [ ] node `8081` 仍监听，作为明确回滚入口。

### 8.2 读取路径

- [ ] 邮箱列表。
- [ ] 邮件列表和正文。
- [ ] HTML 预览和 inline 资源。
- [ ] raw EML。
- [ ] 附件预览和下载。
- [ ] 隔离区列表、原件和附件。
- [ ] 大附件分块、SHA-256 和取消行为。

### 8.3 命令路径

使用专用测试域名或测试邮箱，不在未知生产账号上试验：

- [ ] 域名新增、同步和移除。
- [ ] 邮箱创建、密码修改、软删除、恢复和 purge。
- [ ] 配置 revision 下发与 applied 回报。
- [ ] 过滤 revision 同步。
- [ ] 生命周期和隔离区命令。
- [ ] 超时命令返回 `202 + operation_id` 后能查询最终结果；不能把 `202` 当作失败。

### 8.4 邮件业务

- [ ] SMTP 入站。
- [ ] IMAP 登录与读取。
- [ ] DKIM 签名和 DNS 验证。
- [ ] 转发目标收到测试邮件。
- [ ] 邮件正文、附件和原始邮件内容一致。

只有本节全部通过并记录证据后，才能开始下一台生产节点。

---

## 9. 回滚演练

业务 canary 后必须演练一次显式回滚。先只回滚 system server transport，node 本地继续保持 `dual` 和 `8081`：

```javascript
fetch('/api/v1/admin/servers/<server-id>/transport', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({transport_mode: 'legacy_http'})
}).then(r => r.json()).then(console.log)
```

确认 Legacy 健康探测和至少一条代表性业务路径正常，再用第 7 节接口切回 `dual`。

只有 node 进程因控制通道本身不稳定时，才进一步把本地配置改回：

```yaml
management:
  transport_mode: "legacy_http"
```

然后重启 `mail-node`。回滚时不要删除 `/var/lib/mail-node/identity`，不要撤销 credential，也不要删除后台 server。

---

## 10. 下一台节点

满足以下全部条件后，才能重复第 5-9 节：

- 第一台在 `dual` 下完成全业务 canary。
- 回滚到 `legacy_http` 并再次切回 `dual` 的证据完整。
- 第一台没有持续 session 重连、lease 过期或 revision 偏差。
- 每台节点使用独立邀请、独立 `node_uuid` 和独立 credential。
- 邀请和审批一次只处理一台，不复用 Token。

---

## 11. 最终 control_stream 收口

当前生产完成 `dual` 不等于允许立即进入最终态。以下条件全部满足前，禁止关闭兼容路径：

- 所有生产节点均完成注册、dual canary 和回滚演练。
- 全业务路径在目标环境验证通过。
- 纯 `control_stream` 已先在 staging 或独立 canary 验证。
- 每台节点在本地 `control_stream` 配置下重新连为 `connected/ready`。
- system server transport 已逐台切为 `control_stream`。
- `GET /api/v1/admin/servers/transport-preflight` 返回 fleet 无阻断项。
- 关闭 node `8081` 的网络策略已验证且可以回滚。
- shared secret 清理有独立变更单和恢复方案。

注意：所有节点仍是 `dual` 时，preflight 报告“node is not control_stream”是预期结果，不表示 dual canary 失败。

最终顺序：

```text
逐台 node 本地切 control_stream 并确认重连
  -> 逐台 system server 切 control_stream
  -> fleet preflight 通过
  -> node_control.legacy_http_enabled=false
  -> 阻断 system -> node 8081
  -> 清理 shared secret
```

任一阶段失败，按第 9 节回滚。不得通过直接改数据库绕过 transport 门禁。

---

## 12. 凭证轮转与安装

### 12.1 首次注册和轮转不是同一套交付流程

两种操作都会在控制面签发一枚新的 active credential，但完整明文进入 node 的方式不同：

| 场景 | 控制面行为 | Node 行为 |
|---|---|---|
| 首次注册或身份恢复 | 审批完成时签发 credential | `mail-node enroll` 自动领取并原子写入凭证文件 |
| 已注册节点轮转 | System 只展示一次新 credential；当前 active 进入重叠期 | 运维人员手工替换凭证文件并重启 `mail-node` |

System 数据库只保存 credential 哈希、前缀、版本和状态，不能恢复完整明文。完整 credential 默认只存在于一次性弹窗和 node 的 `/var/lib/mail-node/identity/credential`。若弹窗已关闭且明文没有安全保存，必须再次轮转，不能从数据库或列表中找回。

轮转不会重新注册节点，也不会改变 `node_uuid`、server ID、邮箱、域名或 transport。默认重叠期由全局配置 `node.credential_rotation_overlap_minutes` 控制；当前 active 变为 `rotating`，新 credential 成为 active。重叠期截止或被提前结束后，旧 credential 失效，仍使用旧 credential 的 Control/Data 会话会断开。

### 12.2 从 System 签发新凭证

1. 进入“服务器池”，在目标节点的操作栏点击钥匙图标。
2. 核对节点名称、`node_uuid` 和当前 credential 版本。
3. 点击“轮换凭证”，立即复制一次性弹窗中的完整新 credential。
4. 保持弹窗打开或把 credential 放入组织批准的 Secret 管理工具，直到目标 node 安装完成。

不得把 credential 放入聊天、工单正文、截图、命令参数、文件名或 Shell 历史。`read -rsp` 的引号内容只是提示文字，不能替换成 credential：

```bash
# 正确：执行后等待提示，再粘贴 credential 并按回车；输入不会回显。
read -rsp '粘贴新节点凭证（输入不会显示）: ' NODE_CREDENTIAL

# 错误：credential 会进入 Shell 历史，而且变量仍会等待另一份输入。
read -rsp '<node-credential>' NODE_CREDENTIAL
```

若 credential 已经出现在聊天、历史或日志中，立即停止安装并再次轮转。清理历史只能减少本地残留，不能让已经暴露的 credential 重新变安全。

### 12.3 在 Node 安全替换凭证文件

先确认实际配置路径和凭证文件。本文的标准生产路径是 `/etc/mail-node/config.yaml` 和 `/var/lib/mail-node/identity/credential`；自定义部署以进程实际 `CONFIG_PATH` 及其中的 `management.credential_file` 为准。

正常轮转且旧 credential 仍在重叠期时，可把当前文件临时备份到 root-only 的 `/run`，仅用于新 credential 认证失败时回退：

```bash
install -d -o root -g root -m 0700 /run/mail-node
if test -f /var/lib/mail-node/identity/credential; then
  install -o root -g root -m 0600 \
    /var/lib/mail-node/identity/credential \
    /run/mail-node/credential.before-rotation
fi
```

以下命令可以整块粘贴；Shell 会先解析完整的子 Shell，再停在 `read` 提示处等待 credential。不要把 credential 写进命令本身：

```bash
(
  set -euo pipefail

  identity_dir=/var/lib/mail-node/identity
  temporary="$identity_dir/.credential.new"

  install -d -o root -g root -m 0700 "$identity_dir"
  umask 077
  trap 'rm -f "$temporary"; unset NODE_CREDENTIAL 2>/dev/null || true' EXIT

  read -rsp '粘贴新节点凭证（输入不会显示）: ' NODE_CREDENTIAL
  printf '\n'

  if [[ ! "$NODE_CREDENTIAL" =~ ^mhn_[0-9a-f]{64}$ ]]; then
    echo '凭证格式不正确，未修改正式文件' >&2
    exit 64
  fi

  printf '%s\n' "$NODE_CREDENTIAL" >"$temporary"
  chown root:root "$temporary"
  chmod 0600 "$temporary"
  test "$(wc -c <"$temporary")" -eq 69

  # 使用绝对路径和 -f，避免 root 环境中的 mv -i 别名再次询问覆盖。
  /bin/mv -f -- "$temporary" "$identity_dir/credential"
  trap - EXIT
  unset NODE_CREDENTIAL
)
```

验证文件时只检查存在性、权限和身份元数据，不打印 credential 内容：

```bash
stat -c '%U:%G %a %n' /var/lib/mail-node/identity/credential
mail-node identity show \
  --directory /var/lib/mail-node/identity \
  --credential-file /var/lib/mail-node/identity/credential
```

门禁：文件必须是 `root:root 600`，`identity show` 必须返回 `credential_present=true`，且不得用 `cat`、`head`、`tail` 或调试回显查看完整 credential。

### 12.4 重启和确认新凭证已使用

`mail-node` 在启动时读取控制通道 credential，替换文件后必须重启：

```bash
systemctl restart mail-node
sleep 3
systemctl is-active mail-node
journalctl -u mail-node --since '-2 min' --no-pager -n 80
```

成功日志至少包含新的 Control/Data 建连记录。System 中必须同时满足：

- 目标 server 为 `connection_state=connected`。
- `readiness_state=ready`。
- 新 active credential 的“最近使用时间”已更新。
- lease 持续刷新，ControlStream 和 DataStream 不反复重连。

只有以上门禁全部通过，才可删除临时回退文件，并在 System 中对旧 `rotating` credential 执行“结束轮换”：

```bash
rm -f /run/mail-node/credential.before-rotation
```

结束轮换后，旧 credential 变为 revoked；已 revoked 或 expired 的记录可以从凭证列表软删除，审计历史仍保留。不要点击“撤销全部凭证”，该操作会让节点退出注册状态并停止新分配，不是轮转收尾动作。

### 12.5 失败恢复

若新 credential 安装后无法连接，且旧 credential 仍处于有效重叠期，恢复临时备份并重启：

```bash
test -s /run/mail-node/credential.before-rotation
install -o root -g root -m 0600 \
  /run/mail-node/credential.before-rotation \
  /var/lib/mail-node/identity/credential
systemctl restart mail-node
```

若旧 credential 已过重叠截止时间，恢复旧文件不会重新连接。此时应在 System 再次轮转，安装新一次性 credential 并重启。若所有 credential 已被“撤销全部”，则必须按身份恢复邀请流程处理，不能继续使用普通轮转。

---

## 13. 撤销、恢复和删除边界

| 目标 | 正确操作 | 不应做的操作 |
|---|---|---|
| 暂停新分配 | allocation 设为 draining/disabled | 删除 server |
| transport 回滚 | server 切 `legacy_http` | 撤销 credential |
| 禁用节点身份 | 撤销该 server 的全部 credential | 删除 identity 后继续运行 |
| 恢复同一身份 | 创建身份恢复邀请并人工审批 | 使用普通新节点邀请抢占 UUID |
| 永久退役 | 先迁移邮箱/域名和任务，再按资产流程删除 | 把删除 server 当成“取消注册” |

撤销 credential 会断开控制会话并使节点进入 revoked；恢复必须使用专用恢复邀请。删除 server 是资源生命周期操作，可能影响邮箱、域名和历史关系，不是注册管理动作。

---

## 14. 常见故障

| 现象 | 判断 | 处理 |
|---|---|---|
| `nano: command not found` | 编辑器未安装 | 使用第 5.2 节 `read -s`，不要安装工具来绕过 Secret 规范 |
| `lstat /run/mail-node/mhe_...` | 把 Token 当成文件名 | 撤销已暴露邀请，固定使用 `/run/mail-node/enrollment-token` |
| `test -s` 没有输出 | Token 文件为空 | 重新安全写入，不能继续 enroll |
| `.credential.new` 不存在 | 跳过了 `printf` 写入步骤，或 `read` 实际读到空值 | 使用第 12.3 节完整子 Shell，不能直接从 `chown` 开始 |
| `mv: overwrite ...?` | root 环境把 `mv` alias 为交互模式 | 使用第 12.3 节的 `/bin/mv -f --` 原子替换 |
| 新 active credential 没有“最近使用时间” | 新 credential 尚未安装，或 node 未成功重启建连 | 不得结束旧重叠期；核对文件、服务日志和连接状态 |
| 配置含 `database/auth/node_control` | 正在编辑控制面 YAML | 不要加入 node 的 `management/identity` |
| 服务 active，但无新配置行为 | 文件未保存或 `CONFIG_PATH` 不同 | 检查进程环境和 systemd unit，再重启 |
| 重启后立即 `ss`/`curl` 失败 | 可能处于启动窗口 | 查启动日志并在 listener 启动后复验 |
| `8081` 正常但无 `[control]` | local dual 未生效或 8443 路径卡住 | 核对配置、证书、DNS、同机回环和防火墙 |
| TLS 本机成功、跨机失败 | 网络边界未放行 | 检查云安全组、主机防火墙和来源 CIDR |
| API 返回 lease/ready 错误 | transport 门禁生效 | 先恢复连接、readiness 和 lease，不得改数据库 |
| Token/credential 出现在聊天或历史 | Secret 已暴露 | 立即撤销对应邀请或 credential 并重新签发 |

检查 systemd 实际配置路径：

```bash
MAIL_NODE_PID="$(systemctl show mail-node -p MainPID --value)"
tr '\0' '\n' <"/proc/$MAIL_NODE_PID/environ" | grep '^CONFIG_PATH='
systemctl show mail-node -p ExecStart --no-pager
```

---

## 15. 交接记录模板

交接只记录非敏感字段：

```text
变更版本：
server ID / 名称：
node UUID：
Agent / protocol：
enrollment 状态：
node 本地 transport：
system server transport：
connection / readiness：
desired / applied revision：
Control/Data 建连：
active credential 版本 / 最近使用时间：
旧 credential 重叠截止 / 收尾状态：
业务 canary：
回滚演练：
待办和阻断项：
```

不得记录 Enrollment Token、request secret、credential 明文、shared secret、数据库 DSN、管理员密码、SMTP 密码或 Session Cookie。
