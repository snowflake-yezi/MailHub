# Post-Office-Management-System

> 基于 Postfix + Dovecot 的自建邮局管理系统 —— 融合「宝塔邮局管理器」的多机管理能力与 Roundcube Webmail，可统一管理多台邮件服务器、批量开通邮箱、自动配置域名 DNS / DKIM，并将订单邮件自动汇总转发。

面向国际订票等需要与外部航司 / 供应商 / OTA 邮件通信的场景：为每个业务邮箱提供独立收发能力，非垃圾邮件统一汇总到集成邮箱供运营查看，同时对外提供 API 供大模型系统拉取邮件原件做后续解析。

---

## 核心特性

- **多服务器邮局池**：一台控制面调度 N 台数据面邮件服务器，横向扩容；服务器按健康度参与邮箱分配。
- **域名池 + DNS 一键生成**：每台服务器管理自己的域名，添加域名时自动配置 Postfix 虚拟域、生成 DKIM 密钥并返回 A / MX / SPF / DKIM / DMARC 完整 DNS 清单，运营只需到 DNS 控制台粘贴。
- **邮箱账号全生命周期**：单个 / 批量 / CSV 创建，密码持久化、远端同步状态追踪；安全软删除（回收站 + 定时 GC）。
- **过滤引擎**：可配置规则（按发件人 / 主题等匹配 pass / flag / block），数据面定时拉取，热生效。
- **自动转发汇总**：数据面异步扫描 Maildir，按规则过滤后 SMTP 转发到集成邮箱；正文原样透传，仅改 Subject 加来源标识，内置防循环。
- **Roundcube Webmail**：集成邮箱可选的 Webmail 前端，运营统一登录查看汇总邮件。
- **对外 API**：出票中心 / 大模型系统通过 Token 鉴权创建邮箱、按 scope 拉取邮件原件。

---

## 系统架构

```
┌──────────────────────────────────────────────────────┐
│                mgmt-system（控制面 · 1 套）             │
│   Web 后台 / 邮箱 CRUD / 服务器池 / 域名池+DNS /         │
│   过滤规则 / 邮件查询 API / MIME 预处理                 │
└──────────────────────┬───────────────────────────────┘
                       │ 内部 API（过滤规则 / 心跳 / 域名同步）
            ┌──────────┴──────────┐
            ▼                     ▼
   ┌─────────────────┐   ┌─────────────────┐
   │   mail-node ①   │   │   mail-node ②   │   …… N 台数据面
   │ Postfix/Dovecot │   │ Postfix/Dovecot │
   │ OpenDKIM        │   │ OpenDKIM        │
   └────────┬────────┘   └─────────────────┘
            │ SMTP 转发（按过滤规则）
            ▼
        集成邮箱（运营通过 Roundcube 统一查看）

外部调用方：出票中心 / 大模型系统 ──Token──▶ mgmt-system API（创建邮箱 / 拉取邮件原件）
```

- **控制面 `mgmt-system`**：Go + gin + gorm，Web 后台（Go template + htmx）+ 对外/内部 API，负责编排，不直接处理邮件正文投递。
- **数据面 `mail-node`**：与 Postfix / Dovecot / OpenDKIM 同机部署，负责真实收发、Maildir 管理、过滤转发、域名与 DKIM 落地。

---

## 技术栈

| 层面 | 选型 |
|------|------|
| 后端 | Go 1.22+（gin + gorm） |
| 数据库 | MySQL 8.0（控制面管理数据） |
| 邮件服务 | Postfix + Dovecot + OpenDKIM（数据面自建） |
| 前端（后台） | Go template + htmx |
| Webmail | Roundcube |
| 部署 | 裸机 systemd + Nginx 反代 |

---

## 目录结构

```
.
├── mgmt-system/            # 控制面：管理后台 + API
│   ├── cmd/server/         # 程序入口
│   ├── internal/
│   │   ├── handler/        # HTTP handler（admin / mailbox / server / email / filter）
│   │   ├── service/        # 业务（邮箱创建、账号导入、分配器）
│   │   ├── store/          # gorm 数据访问
│   │   ├── middleware/     # 鉴权等
│   │   ├── model/          # 数据模型
│   │   └── config/         # 配置加载
│   ├── template/           # Go template + htmx 后台页面
│   └── config.example.yaml # 配置模板
├── mail-node/              # 数据面：邮局 agent
│   ├── cmd/node/
│   ├── internal/
│   │   ├── mailbox/        # Maildir 管理、邮箱创建、生命周期
│   │   ├── forward/        # 过滤 + SMTP 转发 + 防循环
│   │   └── config/
│   └── config.example.yaml
└── docs/                   # 设计文档 / 架构概览 / 部署指南
```

---

## 快速开始

### 1. 准备

- 一台控制面机器（MySQL 8.0+）、至少一台数据面机器（开放 25 端口）
- 一个邮件域名，可在 DNS 控制台管理解析

### 2. 配置

复制并填写配置模板（含占位值说明）：

```bash
cp mgmt-system/config.example.yaml  mgmt-system/config.yaml   # 改 DSN、API Token、域名
cp mail-node/config.example.yaml    mail-node/config.yaml     # 改控制面地址、SMTP、转发目标
```

### 3. 构建

```bash
# 控制面
cd mgmt-system && go build -o mgmt-server ./cmd/server

# 数据面（交叉编译到 Linux）
cd mail-node && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o mail-node ./cmd/node
```

### 4. 部署数据面邮件服务

在数据面机器安装 Postfix + Dovecot + OpenDKIM 并注册到控制面。完整步骤见 **[部署指南](docs/design/deployment-guide.md)**。

### 5. 启动

两台机器分别用 systemd 启动（服务文件模板见部署指南），访问管理后台即可开始添加域名、开通邮箱。

---

## 文档

- [架构概览](docs/architecture-overview.md)
- [部署指南](docs/design/deployment-guide.md)
- [转发模块设计](docs/design/forwarding-design.md)
- [服务器域名池设计](docs/design/t4-t5-server-domain-pool-design.md)
- [技术实现方案](docs/design/technical-implementation.md)
- [Roundcube 参考分析](docs/roundcube-analysis.md)

---

## 项目状态

正在迭代中：邮箱账号管理、多服务器域名池、自动转发、Webmail 已就绪；管理后台鉴权、服务器健康检查、MIME 结构化预处理等控制面能力持续完善中。
