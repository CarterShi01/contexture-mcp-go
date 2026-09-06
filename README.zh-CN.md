# Contexture Go 实现

[English](README.md)

Contexture 的 Go 实现。Contexture 是一个面向 MCP 应用的渐进披露框架，用于在
能力不断增长时保持上下文可导航。

语言实现：
[Python](https://github.com/CarterShi01/contexture-mcp) ·
[TypeScript](https://github.com/CarterShi01/contexture-mcp-typescript) ·
[Go](https://github.com/CarterShi01/contexture-mcp-go) ·
[跨语言规范](https://github.com/CarterShi01/contexture-mcp/tree/master/spec)

> **当前状态：正在推进的 0.12 产品移植，尚不是 Python 的可发布替代品。** 内核已有
> 定向执行证据；本仓库现已具备原生项目命令、inspection、可生成的应用、真实 MCP
> launcher、经过认证的请求级根选择与维护中的 demo。完整文档和场景映射、以及干净
> 检出环境的发布审计仍待完成；请勿将当前分支视为完整产品等价。

## 节点模型

不依赖 SDK 的公开根 facade 暴露封闭节点集合；实现按职责分布在
`core/model/` 下：

- `Role`：职责与容器边界；
- `Skill`：由模型遵循的操作过程；
- `Tool`：拥有一份强类型 Binding 的可执行能力；
- `Node`：只能由上述指针类型实现的封闭接口。

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

业务 Tool 始终位于 Contexture 的四个固定网关 Tool 后面。根包不依赖 SDK；
`server` 包拥有官方 MCP Go SDK，`web` 包拥有显式 `net/http` REST 适配器。请求级事实通过
`context.Context` 传递，应用依赖通过 `Channels` 管理并按逆序清理。

`contexture.Contexture(declaration)` 是 `contexture.DeclareApplication` 的具名公开别名；
两者创建相同的惰性 application 声明。

## 检查 Agent 可见 context

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

该移植锁定 `conformance/specification.json` 中记录的 Contexture Specification
0.12 提交。固定 fixtures 和 golden 输出保存在 `conformance/`；测试会先通过
Go 实现生成真实观察结果，再与这些资产比较。上述命令验证的是已实现的内核，
不是完整产品的发布 gate。

## 仓库结构

架构文档也提供[英文原文](docs/architecture.md)。

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
