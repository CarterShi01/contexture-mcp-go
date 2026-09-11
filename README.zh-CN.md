# Contexture Go 实现

[English](README.md)

Contexture 的 Go 实现。Contexture 是一个面向 MCP 应用的渐进披露框架，用于在
能力不断增长时保持上下文可导航。

语言实现：
[Python](https://github.com/CarterShi01/contexture-mcp) ·
[TypeScript](https://github.com/CarterShi01/contexture-mcp-typescript) ·
[Go](https://github.com/CarterShi01/contexture-mcp-go) ·
[跨语言规范](https://github.com/CarterShi01/contexture-mcp/tree/master/spec)

> **当前状态：`v1.0.0` 已作为首个公开稳定 Go 版本准备完毕。** 已记录的公开 package
> 对 v1 版本线作出 source-compatibility 承诺。已验证证据覆盖固定的 Contexture 0.16
> 合同及全部适用产品条目；公开的产品等价声明仍以当前 Host 和发布 gate 为条件，
> 创建不可变的 `v1.0.0` tag 仍须单独获得维护者授权。

公开 package 包括 module root、`core/model`、`server`、`server/surface`、`web`、`demo`、
`inspection` 与 `cli`。发布检查会创建独立 Go module，并通过本地 module replacement 导入每个 package。

## 安装 v1

在 `v1.0.0` tag 发布后，使用下列命令将 library 加入 application：

```bash
go get github.com/CarterShi01/contexture-mcp-go@v1.0.0
```

使用下列命令安装项目 CLI：

```bash
go install github.com/CarterShi01/contexture-mcp-go/cmd/contexture@v1.0.0
```

## 节点模型

不依赖 SDK 的公开根 facade 暴露封闭节点集合；实现按职责分布在
`core/model/` 下：

- `Role`：职责与容器边界；
- `Skill`：由模型遵循的操作过程；
- `Tool`：拥有一份强类型 Binding 的可执行能力；
- `Node`：只能由上述指针类型实现的封闭接口。

### 可选过程成员

当准备或收尾需要单独披露的流程和设备时，把 `Role.PreProcess` 和/或
`Role.PostProcess` 设为返回对应强类型的惰性 factory。两者在线上仍是 kind `role`，可包含
普通 Role、Skill、Tool 以及显式嵌套的过程成员；它们是过程设备，不是可替代的 child
branch，也不是自动 callback。ACTIVE 会在不修改业务 instructions 的前后分别组合固定的
PreProcess/PostProcess 合约；打开过程成员本身只披露流程。只有显式 Tool 调用才会产生副作用。
框架级 `Publication` 与 `Role.Publication` 已无别名地移除，应用必须迁移到
`PostProcess` 与 `Role.PostProcess`。

公开的 `BindingInstruction(source, body, action)` 可标记应用自己的硬规则；source 必须说明
应用侧权威，并且不能冒充 Contexture 框架。

`NewTool` 和 `NewToolWithSchema` 由 `core/model/binding.go` 支持。根 facade
只暴露声明；MCP 与 web 适配器需要显式单独导入。

## 示例

```go
package main

import (
	"context"
	"log"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

type statusInput struct {
	Service string `json:"service"`
}

func main() {
	status, err := contexture.NewTool(
		"status",
		"Return one service status.",
		true,
		func(_ context.Context, input statusInput) (map[string]any, error) {
			return map[string]any{"service": input.Service, "healthy": true}, nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "operations",
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{
				Name:         "operations",
				Description:  "Operate services.",
				Instructions: "Inspect before changing anything.",
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{
						Name:         "diagnose",
						Description:  "Diagnose an unhealthy service.",
						Instructions: "Read status and explain the evidence.",
						Uses:         []string{"operations/status"},
					}
				}},
				Tools: []contexture.Factory{func() contexture.Node { return status }},
			}
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	index, err := contexture.Compile(application)
	if err != nil {
		log.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(
		index, contexture.AllRoots(), contexture.AllRoots(), nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	gateway, err := contexture.NewGateway(disclosure, runtime)
	if err != nil {
		log.Fatal(err)
	}
	adapter := server.NewContextureMCPServer(
		server.Identity{Name: "operations", Version: "0.1.0"}, gateway,
	)
	_ = adapter.Server // 由 Host 将其连接到官方 MCP SDK transport。
}
```

业务 Tool 始终位于 Contexture 的五个固定网关 Tool 后面：`contexture_discover`、
`contexture_inspect`、`contexture_open`、`contexture_invoke_read_only` 与
`contexture_invoke`。`contexture_inspect` 原子比较 1–32 个唯一 ref，只返回目标、
直接成员和声明 uses 的纯路由卡；它不激活、不调用，也不披露 instructions、执行 facet
或框架过程合约。根包不依赖 SDK；
`server` 包拥有官方 MCP Go SDK，`web` 包拥有显式 `net/http` REST 适配器。请求级事实通过
`context.Context` 传递，应用依赖通过 `Channels` 管理并按逆序清理。

`contexture.Contexture(declaration)` 是 `contexture.DeclareApplication` 的具名公开别名；
两者创建相同的惰性 application 声明。

## 检查 Agent 可见 context

MCP 网关的 `Gateway.Inspect` 与下述既有 CLI 命令彼此独立。CLI 仍用于重放完整的
Agent 可见会话，其行为和参数保持不变。

`contexture inspect` 会重放原生实现生成的准确 instructions、discovery payload 和
渐进披露卡片，不会启动 MCP transport。修改声明后、连接 Host 前使用它：

```bash
go run ./cmd/contexture inspect operations --all --summary
go run ./cmd/contexture inspect operations/runbook --read
go run ./cmd/contexture inspect --all --json > contexture-trace.json
```

`--all` 按 Role 的广度优先顺序逐一遍历每个可见 ref；`--summary` 保留成本和 Host
限制检查、隐藏 payload body；`--json` 生成可供 CI 比对的稳定 trace。`--read` 会额外
调用一个无参数、只读的内容 Tool，因此只应在确实需要本地读取时使用。未找到项目配置且
未指定 target 时，`inspect` 会重放内置 demo，并在 stderr 报告此回退。

## 创建并运行项目

原生命令会创建唯一支持的 `project` 模板。生成的 application 自己拥有本地工作流，
因此应在新项目中执行这些命令，而不是在本仓库中执行：

```bash
contexture new operations --template project
cd operations
go mod tidy
go run ./cmd/assistant check
go run ./cmd/assistant list
go run ./cmd/assistant inspect --all --summary
go run ./cmd/assistant call operations/ping --input '{"target":"local"}'
```

`contexture new` 会拒绝已经存在的目标目录和未知模板。生成项目的 `check` 会校验而不
打开 application dependency；`call` 复用正式服务相同的 runtime Binding，写 Tool 则必须
显式传入 `--allow-write`。

## Host 配置

Host 配置应当指向启动服务器的命令，而不是复制应用已经声明的 context。`server.Launch`
可以生成 Claude Code、Cursor 和 Codex 所需的准确格式：

```go
launch := server.Launch{
	Name: "operations", Command: "go",
	Args: []string{"run", "./cmd/assistant", "serve"},
}
fmt.Print(server.ClaudeCodeConfig(launch)) // .mcp.json 或 .cursor/mcp.json
fmt.Print(server.CodexConfig(launch))      // ~/.codex/config.toml 的 stanza
```

`server.CLICommands(launch)` 会返回经过安全 shell 引用的 `claude mcp add` 与
`codex mcp add` 命令。使用自定义 stdio 入口的应用也可复用同一 API。

对于 streamable HTTP，`HeaderSurfaceSelector` 读取规范的 `Contexture-Select`
请求头。逗号分隔的直接路径（如 `team/notebook-editor`）或末尾通配符（如
`team/*`）会把每个解析结果提升为请求级 surface root。selection 不会扩大 runtime
或 identity ceiling。旧的 `Contexture-Roots` 请求头仍受支持，但新集成应发送
`Contexture-Select`。无效 selector 会返回安全的 JSON-RPC `-32602` 响应，并保留请求 ID。

## 运行项目

Go CLI 运行项目 `cmd/assistant/main.go` 中静态声明的 application；它不会依据字符串
任意导入源文件。

```bash
go run ./cmd/contexture new my-context
cd my-context
go mod tidy
go run ./cmd/assistant check
go run ./cmd/assistant list
go run ./cmd/assistant inspect --all --read --summary
go run ./cmd/assistant serve                    # MCP stdio；会阻塞
go run ./cmd/assistant serve --transport streamable-http
```

在生成项目内，已安装的 `contexture` 命令会把同样的 `check`、`list`、`inspect`、
`call` 与 `serve` 工作流转发给该 application。`call` 默认拒绝写 Tool，除非显式传入
`--allow-write`；JSON 参数只能来自 `--input` 或 `--input-file` 其中之一。

维护中的确定性 Kubernetes 应用可通过以下方式运行：

```bash
go run ./cmd/contexture demo                    # MCP stdio；会阻塞
go run ./cmd/contexture demo --transport streamable-http
```

## 开发检查

需要 Go 1.25 或更新版本。

```bash
git clone https://github.com/CarterShi01/contexture-mcp-go.git
cd contexture-mcp-go
go mod download
go run ./internal/conformancecheck
go test -race ./...
go vet ./...
```

本实现锁定 `conformance/specification.json` 中记录的 Contexture Specification
0.16 提交。固定 fixtures 和 golden 输出保存在 `conformance/`；测试会先通过
Go 实现生成真实观察结果，再与这些资产比较。

## 仓库结构

请阅读 [Go 使用手册](docs/handbook.zh-CN.md)、其[英文原文](docs/handbook.md)和
[架构文档](docs/architecture.zh-CN.md)。真实 Host 证据与复现步骤记录在
[Host verification](docs/verification/hosts.md) 中。

```text
facade.go        面向声明的公开、SDK-neutral facade
core/foundation/ 错误与锁定的规范身份
core/mcpinterface/ SDK-neutral Prompt、Resource 与 gateway 声明
core/model/      编译、披露、运行时、生命周期与 Binding
server/          MCP SDK 适配器与发布表面
web/             显式 HTTP route 与 REST 适配器
conformance/    固定规范身份、fixtures 与 golden 数据
```

英文是项目第一语言；简体中文文档作为翻译持续维护。

## 许可证

Apache-2.0，参见 [LICENSE](LICENSE)。
