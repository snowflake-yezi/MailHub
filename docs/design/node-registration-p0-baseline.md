# NR-P0 基线与协议冻结记录

> 状态：已建立
>
> 日期：2026-07-24
>
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 改造前基线

仓库在 NR-P0 开始时包含三个 Go module，均声明 `go 1.22.0`：

| Module | 路径 | 基线验证 |
|---|---|---|
| `github.com/ticket/email-filter-contract` | `filter-contract` | `go test ./...` 通过 |
| `github.com/ticket/email-mgmt-system` | `mgmt-system` | `go test ./...` 通过 |
| `github.com/ticket/email-mail-node` | `mail-node` | `go test ./...` 通过 |

管理后台基线：

- `npm test` 通过；i18n 为 3 种语言、911 个键；
- UI contract 检查通过；
- `npm run build` 通过；
- 生产构建产物仍由现有 Vite 流程写入 `mgmt-system/template/static/admin-app`。

当前 legacy 通信基线不变：

```text
api_host + shared_secret + system -> node:8081 HTTP + node -> system HTTP
```

## 2. 固定工具链

| 工具或 runtime | 固定版本 |
|---|---:|
| `protoc` | 28.3 |
| `protoc-gen-go` | 1.35.2 |
| `protoc-gen-go-grpc` | 1.5.1 |
| `google.golang.org/protobuf` | 1.35.2 |
| `google.golang.org/grpc` | 1.68.1 |

机器可执行工具版本以 `node-contract/tools/versions.json` 为唯一来源；生成器会拒绝不匹配的版本。Go runtime 版本由 `node-contract/go.mod` 固定。

## 3. 冻结内容

`node-contract` 冻结以下 V1 契约：

- `NodeGateway.Control` 与 `NodeGateway.Data` 两条独立双向流；
- Control/Data 双向 frame、字段号、oneof 和枚举数值；
- enrollment、connection、readiness、allocation、transport 和 command 状态；
- 第一批持久化命令、revision 通知和 DataStream 请求类型；
- 注册、审批、凭证轮换与撤销路由；
- 鉴权 metadata 名称、协议版本和 256 KiB data chunk 上限。

完整 Protobuf descriptor 使用确定性 SHA-256 兼容测试锁定。任何协议变更都必须先审查兼容性，再显式更新摘要。

## 4. 生成与验收命令

从 `node-contract` 执行：

```text
go generate ./...
go test ./...
```

第二次执行 `go generate ./...` 后必须无 diff。完整 NR-P0 回归还包括三个原有 Go module 的 `go test ./...`、Web `npm test` 和 `npm run build`。

## 5. Feature Flags 基线

- system：`node_control.enabled=false`、`node_control.legacy_http_enabled=true`；
- node：`management.transport_mode=legacy_http`、`management.control_url` 默认为空；
- 新配置当前只建立兼容边界，不启动 gRPC listener、Agent 或注册业务；
- NR-P1 及后续实现不得在 flag 关闭时改变 legacy 分配或通信行为。
