# NR-P2 注册、审批与每节点凭证验收记录

> 状态：已完成（2026-07-24）
>
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 交付范围

- 管理端邀请创建、列表、撤销，支持标准注册、UUID 预绑定、自动批准约束和专用恢复邀请。
- bootstrap `claim/status/complete` API：Enrollment Token、request secret 和节点 credential 均只保存 SHA-256 摘要及展示前缀，明文只在对应创建响应中出现一次。
- 审批在事务内创建或恢复 `mail_servers` 绑定；新节点默认 `allocation=disabled`，不会因注册完成直接进入分配池。
- 每节点 credential 支持短期新旧重叠轮换、全部撤销、最近使用时间和管理员审计。
- `/api/v1/internal/*` 同时接受 legacy shared secret 与 `Authorization: Node`；节点凭证路径校验 URL、query 和 JSON body 中的 server/node ID。
- `mail-node identity init/show`、`enroll` 和 `enroll resume`；request secret、credential 使用 root-only 文件和落盘后原子替换。
- 服务器池增加邀请、审批、UUID/四维状态、凭证轮换与撤销操作，新增文字完成中英日三语覆盖。

## 2. 安全边界

- 审批只绑定节点身份；运行 credential 在 node 使用 request secret 调用 `complete` 时生成并原子保存哈希。这样无需在审批和领取之间保存可还原的凭证明文。
- `complete` 只允许一次。节点先持久化 credential，再删除 resume 状态；中断时可继续 `enroll resume`。
- 恢复邀请显式绑定已有 server ID 与 UUID，不能通过 IP、`api_host` 或普通预绑定邀请认领已有节点。
- 管理审计只记录邀请/凭证前缀、版本、UUID 和状态，不记录三类 secret。
- NR-P2 不启动 gRPC、不切换 transport、不关闭 node `8081`，现有 legacy 数据面保持不变。

## 3. 自动化证据

- 服务事务测试覆盖：标准注册、批准前禁止领取、单次领取、错误 UUID、轮换重叠、撤销、邀请过期/撤销、预绑定不匹配、拒绝和带历史申请的重装恢复。
- 中间件测试覆盖：shared secret 兼容、节点 credential 成功认证以及 path/query/body 跨节点访问拒绝。
- 节点测试覆盖：HTTPS 强制、指定 CA、Token 只进 body、request secret 只进 Authorization、pending 状态恢复、credential 先落盘后清理和身份目录外写入拒绝。
- Web 验证覆盖：三语键一致、注册 UI contract 与 Vite production build。

完整回归命令及最终结果记录在本次变更的验证输出中。目标 Unix 权限和 MariaDB 生产副本验证按既定决策留到 NR-P7 发布验收。

## 4. 下一步

进入 NR-P3：先定义并接入 `NodeTransport`，把所有 system -> node 的直接 HTTP 调用收口到 `LegacyHTTPTransport`；在该阶段仍不得提前实现 gRPC handler 或改变业务状态语义。
