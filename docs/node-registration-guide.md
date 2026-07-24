# Mail-node 节点注册与加入集群指南

> 文档类型：目标运维指南
>
> 适用方案：[Mail-node 注册、身份与出站控制通道设计](design/node-enrollment-control-channel-design.md)
>
> 实现状态：已批准，待实施。当前版本仍使用 `api_host + shared_secret + 双向 HTTP`；在注册功能交付前，请继续使用[数据面部署指南](design/deployment-guide.md)。开发顺序见[节点注册发现与出站控制通道实施计划](design/node-registration-control-channel-implementation-plan.md)。本文中的页面和命令暂不可直接用于当前生产环境。

---

## 1. 注册模型

节点注册不是在 system 后台手工填写一个 IP，然后默认信任该机器。完整流程是：

```text
system 创建短期注册邀请
  -> node 生成永久 UUID
  -> node 使用邀请发起加入申请
  -> system 展示 UUID、请求标识和机器信息
  -> 管理员批准
  -> system 为该 node 签发独立运行 Token
  -> node 建立出站控制通道
  -> 双方完成健康和能力校验
```

每个标识的用途不同：

| 标识 | 生成方 | 是否持久 | 用途 |
|---|---|---:|---|
| `node_uuid` | node | 是 | 一台节点的永久身份 |
| Enrollment Token | system | 否 | 允许一台新机器在短时间内申请加入 |
| request secret | system | 审批期间 | 只允许申请方轮询并领取一次运行凭证 |
| 节点运行 Token | system | 有效期内 | 本轮运行鉴权，可单节点轮换和撤销 |
| `boot_id` | node 进程 | 否 | 区分每次进程启动 |
| `session_id` | system | 否 | 区分每次控制通道连接 |

最终的 system 节点记录必须包含 `node_uuid`，但标准注册模式不要求管理员提前复制 UUID。

---

## 2. 两种注册模式

### 2.1 标准审批注册（推荐）

管理员先创建一个未绑定 UUID 的短期邀请。node 使用邀请申请加入后，system 显示实际 UUID、请求 ID、主机名、来源 IP 和版本，管理员核对后批准。

适合日常新增服务器、云主机、动态 IP 节点和自动扩容场景。这种方式减少了人工复制 UUID 导致的抄错和错绑。

### 2.2 严格预绑定 UUID

运维人员先在 node 初始化身份并读取 UUID，再由管理员创建只允许该 UUID 使用的邀请。如果申请中的 UUID 不匹配，system 必须拒绝。

适合受监管机房、资产已提前备案、安装与审批职责分离，或必须提前锁定机器身份的环境。

UUID 只是注册约束，不是密码。即使使用严格模式，也必须校验 Enrollment Token、TLS 服务端身份和批准后签发的节点独立凭证。

---

## 3. 注册前准备

### 3.1 system 侧

- `mgmt-system` 已通过受信任 HTTPS 地址提供服务。
- 控制通道入口支持 TLS/443；使用 gRPC 时反向代理支持 HTTP/2 和长连接。
- 管理员拥有节点注册、审批和凭证管理权限。
- system 时间同步正常。
- 已配置受信任的 TLS 服务端证书和每节点 Token 哈希存储。

### 3.2 node 侧

- `mail-node` 已安装，但尚未以生产模式接管邮箱。
- node 能够出站访问 system 的 HTTPS/443。
- node 时间同步正常。
- `/var/lib/mail-node/identity` 只能由运行用户和 root 访问。
- node 已安装正确的控制面服务端 CA，或系统公共 CA 信任链有效。
- Postfix、Dovecot、OpenDKIM、Maildir 和磁盘权限已按部署指南准备。

### 3.3 完成迁移后的网络边界

- system 不需要主动访问 node 的 `8081`。
- node 不需要开放管理 UI。
- node 不需要固定公网 IP 来维持身份。
- SMTP、IMAP 等邮件业务端口仍按业务需要开放，不能与控制通道混为一谈。

---

## 4. 标准审批注册

### 步骤 1：在 system 创建注册邀请

进入：

```text
管理后台 -> 服务器池 -> 注册节点 -> 创建注册邀请
```

填写：

| 字段 | 建议值 | 说明 |
|---|---|---|
| 节点名称 | `mail-node-cn-01` | 期望显示名称，不作为永久身份 |
| 环境 | `production` | 防止测试节点加入生产池 |
| 区域 | `cn-east-1` | 用于展示和后续调度 |
| 标签 | `provider=example` | 可选的资产或调度标签 |
| 有效期 | 15 分钟 | 应尽可能短 |
| 最大使用次数 | 1 | 单节点邀请默认只能使用一次 |
| 自动批准 | 关闭 | 生产环境必须人工核对 |
| 预期 UUID | 留空 | 标准模式由 node 申请时提交 |

页面一次性显示：

- Enrollment Token；
- system 地址；
- TLS CA 指纹或 CA 下载入口；
- Token 过期时间；
- 推荐的 node 注册命令。

不要把 Token 写入工单、聊天记录、Shell 历史或普通配置文件。应通过受控 Secret 分发渠道交给 node 安装人员。

### 步骤 2：在 node 保存一次性 Token

建议将 Token 写入临时 root-only 文件：

```bash
install -d -m 0700 /run/mail-node
install -m 0600 /dev/null /run/mail-node/enrollment-token
```

使用安全终端编辑该文件，只写入完整 Token。不要通过 `--token <明文>` 传参，避免出现在进程列表和 Shell 历史中。

### 步骤 3：执行注册

目标命令：

```bash
mail-node enroll \
  --management-url https://mailhub.example.com \
  --token-file /run/mail-node/enrollment-token \
  --ca-file /etc/mail-node/management-ca.pem \
  --name mail-node-cn-01
```

该命令应完成：

1. 校验 system TLS 证书和主机名。
2. 身份目录不存在时生成 `node_uuid`。
3. 使用 Enrollment Token 提交节点 UUID 和机器信息。
4. 获得只用于本次申请的 request secret，并写入 root-only 临时状态文件。
5. 输出注册请求 ID、UUID 和 `pending_approval` 状态。

示例目标输出：

```text
Registration request submitted
Request ID: enr_01J...
Node UUID: b542fd12-2c3d-4f02-b8d4-6dd7f61f9140
Status: pending_approval
```

命令不得在未批准时降级使用全局 shared secret，也不得忽略 TLS 校验。

### 步骤 4：在 system 核对申请

进入：

```text
管理后台 -> 服务器池 -> 待审批节点
```

至少核对：

- 页面 UUID 与 node 终端输出一致；
- 页面 Request ID 与 node 终端输出一致；
- 主机名、操作系统、架构和 Agent 版本符合资产记录；
- 来源网络符合预期；
- 使用的邀请由正确管理员创建且未过期；
- 环境、区域和标签正确；
- 没有相同 UUID 的在线节点或未处置旧记录。

确认后点击“批准并签发凭证”。任何信息不符时，应拒绝申请、撤销邀请并调查，不能通过修改后台 UUID 强行匹配。

### 步骤 5：node 领取凭证并建连

node 可以等待批准结果，也可以由运维人员执行目标命令：

```bash
mail-node enroll resume --request-id enr_01J...
```

批准后 node 应：

1. 使用 request secret 一次性领取节点运行 Token。
2. 校验响应绑定的 UUID 与本地 UUID 一致。
3. 将运行 Token 原子写入 root-only 身份目录。
4. 删除 request secret 和临时 Enrollment Token。
5. 通过 TLS 建立带节点 Token 鉴权的出站控制通道。
6. 上报版本、能力、健康和配置 revision。

注册完成后删除临时文件：

```bash
rm -f /run/mail-node/enrollment-token
```

Token 也应在首次成功使用后立即失效。

### 步骤 6：验证节点

在 node 执行目标命令：

```bash
mail-node status
mail-node doctor
```

目标状态：

```text
Enrollment: approved
Credential: valid
Connection: connected
Readiness: ready
Allocation: draining
Config revision: desired=42 applied=42
```

新节点注册后默认处于 `draining`。完成以下检查后再启用分配：

- Postfix、Dovecot、OpenDKIM 自检通过；
- Maildir 路径、UID/GID 和磁盘容量正确；
- node 与 system 时间偏差在允许范围；
- 测试命令可以成功下发并返回；
- desired/applied revision 一致；
- 测试域名和测试邮箱流程通过。

最后在 system 节点详情页执行“启用分配”，将 allocation 状态切换为 `active`。

---

## 5. 严格预绑定 UUID

### 步骤 1：在 node 初始化并读取身份

目标命令：

```bash
mail-node identity init
mail-node identity show
```

示例目标输出：

```text
Node UUID: b542fd12-2c3d-4f02-b8d4-6dd7f61f9140
Identity path: /var/lib/mail-node/identity
Enrollment: not enrolled
Credential: absent
```

`identity init` 必须幂等：身份已存在时只显示现有 UUID，不能静默生成新身份。

### 步骤 2：在 system 创建预绑定邀请

创建邀请时填写：

```text
预期 UUID：b542fd12-2c3d-4f02-b8d4-6dd7f61f9140
```

system 必须保证：

- 同一邀请只能绑定一个 UUID；
- UUID 已属于有效节点时不能创建普通新增邀请；
- 申请 UUID 不匹配时拒绝并产生安全审计事件；
- 管理员不能在申请到达后无审计地修改预期 UUID。

### 步骤 3：执行 enroll 并审批

执行与标准模式相同的 `mail-node enroll`。管理员仍需核对 Request ID 和机器信息。UUID 匹配只是必要条件，不是充分条件。

---

## 6. 注册后的 system 页面

节点详情页应至少显示：

```text
身份
  - node_uuid
  - 数值 server_id（内部数据库引用）
  - 注册状态和注册时间
  - 首次/最近来源地址

凭证
  - Token 前缀和凭证版本
  - 签发、到期、最近使用和最近轮换时间
  - 凭证代次和撤销状态

连接
  - connected/disconnected
  - session_id、boot_id、started_at
  - Agent 与协议版本、capabilities

运行
  - ready/degraded/failed
  - active/draining/disabled
  - desired/applied revision
  - 最近命令和 Apply 错误
```

地址只作为诊断信息，不作为身份和授权依据。

---

## 7. 常见问题

### system 添加 node 时到底要不要 UUID？

正式节点记录必须有 UUID。标准模式下创建邀请时不需要提前填写，node 申请后由 system 记录并等待审批；严格模式下创建邀请时需要填写预期 UUID。

### UUID 是不是密钥？

不是。UUID 可以显示在后台和日志中，只提供稳定标识。本轮真正证明运行身份的是 system 签发、node 持有的独立运行 Token。

### 为什么不由 system 生成 UUID 再复制给 node？

这样容易形成“知道 UUID 就能认领身份”的错误实现。推荐由 node 本地生成 UUID，再通过一次性邀请、管理员审批和节点独立凭证完成绑定。

### IP 改了需要重新注册吗？

不需要。UUID 和节点运行 Token 未改变时，node 使用原凭证从新地址建立连接即可。

### node 重启需要重新注册吗？

不需要。重启只改变 `boot_id`，不会改变 `node_uuid` 和凭证。

### Token 过期了怎么办？

在 system 撤销旧邀请并创建新邀请。不要延长已经暴露或来源不明的 Token。

### 注册时 system 不在线怎么办？

enroll 返回可重试错误并保留本地 UUID。system 恢复后用仍有效的 Enrollment Token 重试；邀请已过期则创建新邀请。

### node 需要独立 UI 吗？

不需要。审批、配置和状态统一在 system UI；node 只提供本机 CLI、日志、指标和 loopback 健康端点。

---

## 8. 异常与恢复

### 8.1 重复 UUID

如果同一 UUID 同时从两个不同机器指纹或不同凭证建立连接：

1. system 拒绝后建立的可疑连接。
2. 节点进入身份冲突告警并暂停新业务分配。
3. 运维检查是否克隆了身份目录或发生凭证泄露。
4. 对非法副本重置身份并按新节点注册。

不得允许两个会话共享同一 UUID 来临时绕过。

### 8.2 节点系统重装

有安全备份且确认原机器时，恢复整个身份目录及其权限。没有可信节点凭证时：

1. 在 system 将旧节点设为 draining/disabled。
2. 撤销旧凭证。
3. 创建带审计的身份恢复邀请，或按新节点注册。
4. 不要仅凭原 IP 认领旧节点。

### 8.3 替换硬件

新硬件默认注册为新 UUID。邮箱数据迁移、域名切换和旧节点退役是独立流程，不应复制旧节点运行 Token 来伪装成旧节点。

### 8.4 撤销节点

推荐顺序：

```text
停止分配 -> 等待任务排空 -> 撤销凭证 -> 断开会话 -> 标记 revoked
```

撤销后该 UUID 不能使用普通凭证重新加入。确需恢复时必须创建专用恢复邀请并再次审批。

---

## 9. 自动化注册

自动化平台可以通过 system 管理 API 创建短期邀请，再将 Token 注入实例临时 Secret。要求：

- Token 单次使用且短期有效；
- Token 不写入镜像、cloud-init 永久日志和 Terraform state 明文；
- 自动批准只允许在有机器身份保证的环境启用；
- 自动化仍校验环境、区域、镜像版本和资产身份；
- 实例销毁时撤销凭证；
- 克隆镜像中不得包含 `/var/lib/mail-node/identity`。

大规模场景可以把邀请约束绑定到云实例身份文档、SPIFFE/SPIRE 或企业资产系统，但这些属于注册认证增强，不改变 `node_uuid + 独立凭证 + 出站连接` 主模型。

---

## 10. 安全检查清单

- [ ] system 注册入口仅通过 HTTPS 提供。
- [ ] node 校验 system 证书，不启用 insecure skip verify。
- [ ] Enrollment Token 单次使用、短期有效且只保存哈希。
- [ ] Token 通过文件传入，不出现在命令行和日志。
- [ ] request secret 和节点运行 Token 使用 root-only 文件保存。
- [ ] 管理员审批时核对 UUID、Request ID 和机器信息。
- [ ] 每台 node 使用独立凭证。
- [ ] 新节点默认 draining，通过验收后才参与分配。
- [ ] 镜像和快照不包含已有身份目录。
- [ ] 节点撤销、恢复和 Token 轮换均有审计记录。
- [ ] 完成新通道迁移后关闭 node 的反向管理端口。

---

## 11. 实现完成后的验收

目标实现应提供不泄露敏感信息的机器可读输出：

```bash
mail-node identity show --output json
mail-node status --output json
mail-node doctor --output json
```

system 侧应能核对：

```text
节点 UUID 相同
凭证状态 valid
连接状态 connected
节点状态 ready
分配状态符合预期
desired_revision == applied_revision
最近命令无未处理失败
```

注册功能只有在标准审批、严格预绑定、Token 过期、重复 UUID、凭证撤销、断网重试和重装恢复场景全部通过测试后，才能替代当前发现机制。
