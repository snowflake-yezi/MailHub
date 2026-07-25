# NR-P3 NodeTransport 收口设计

> 状态：已完成（2026-07-25）
> 上位计划：[节点注册发现与出站控制通道实施计划](node-registration-control-channel-implementation-plan.md)

## 1. 目标与边界

NR-P3 把 `mgmt-system` 的全部 system -> node 调用收口到 `NodeTransport`，为后续 ControlStream 和 DataStream 提供唯一替换点。本阶段只做 legacy HTTP 等价重构：

- 不实现或启用 gRPC handler、ControlStream、DataStream。
- 不修改外部 API、mail-node 路由、状态码、JSON 信封、二进制响应头和业务状态语义。
- 不修改节点 transport mode、allocation、广告过滤模式或生产配置。
- `api_host` 继续作为 legacy HTTP 地址使用，但不再由业务包拼接 URL。

## 2. 接口与请求模型

`NodeTransport` 提供五类能力：

```go
type NodeTransport interface {
    Execute(context.Context, Target, Command) (*Response, error)
    Notify(context.Context, Target, Notification) (*Response, error)
    Query(context.Context, Target, DataRequest) (*Response, error)
    OpenData(context.Context, Target, DataRequest) (*DataResponse, error)
    Probe(context.Context, Target) (*Response, error)
}
```

- `Target` 同时携带稳定 node ID 与当前 legacy `api_host`。
- `Command`、`Notification` 和 `DataRequest` 使用 `node-contract` 已冻结的类型名，并携带未来流式传输可直接复用的 JSON payload/metadata。
- legacy method、path、body 和超时只存在于 transport 包内的兼容描述中；业务包通过操作构造器创建请求。
- 普通响应完整读取为 `Response`；附件、preview 和 raw EML 使用 `DataResponse` 流式返回。

## 3. LegacyHTTPTransport

`LegacyHTTPTransport` 是 NR-P3 唯一生产实现，负责：

- 统一拼接 node URL、设置 `X-Internal-Token` 和需要时的 JSON Content-Type。
- 保留现有普通 JSON 10 秒、域名 15 秒、配置通知 5 秒、附件 60 秒超时。
- raw EML 只限制连接和响应头等待，不对响应 body 增加总时限。
- 原样返回上游状态码、响应体和响应头，由现有业务适配层继续执行原来的错误映射。

## 4. 接入范围

严格覆盖上位计划第 6.1 节：健康检查、邮箱创建/删除/恢复/改密、域名添加/删除、邮件列表/正文/raw/附件/preview、隔离区读取/附件/放行/状态、生命周期维护，以及配置/过滤/转发重载通知。

`cmd/server` 创建单个 transport 实例并注入 handler、service 和 scheduler。业务包不得自行创建 system -> node HTTP request。

## 5. 验收

- transport 契约测试覆盖 method、path、query、body、鉴权头、错误状态和二进制响应头。
- 原 handler/service/scheduler 回归测试继续验证既有响应映射和幂等语义。
- 静态测试扫描 `handler`、`service`、`healthcheck` 和 `lifecycle`，禁止生产 Go 文件出现直接 `net/http` request 构造或 node URL scheme。
- `mgmt-system` 全量 test/vet、其余 Go 模块全量 test/vet、Web 契约和生产构建通过。

## 6. 完成记录

- 新增 `internal/nodetransport`，`cmd/server` 创建单个 `LegacyHTTPTransport` 并注入全部调用方；上位计划第 6.1 节的 11 个入口均已迁移。
- 操作构造器锁定 `node-contract` 命令、通知和数据请求类型；legacy method/path/body、shared secret 和 HTTP client 只存在于 transport 包。
- 契约测试覆盖邮箱、域名、邮件、隔离区、生命周期、通知和健康探测；延迟流测试确认附件在收到响应头后仍能继续读取 body。
- 静态检查确认 `handler`、`service`、`healthcheck` 和 `lifecycle` 不再拼接 node URL，也不直接调用 `http.NewRequest*`。
- 最终验证通过：四个 Go 模块全量 `go test ./...`、`go vet ./...`、`go mod verify`；P3 的 transport/handler/lifecycle/service race；Web 1014 个三语键、UI contract 和 Vite production build。Vite 仅有既有的 500 kB chunk warning。
- 未部署、未启用 gRPC、未切换 transport mode，生产兼容边界保持 legacy HTTP 不变。
