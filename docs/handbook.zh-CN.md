# Contexture Go 使用手册

Contexture 让不断增长的 MCP application 仍保持可导航。模型先看到简短的路由卡片，
逐层打开相关分支，最后才获得 Skill 流程或 Tool schema。它不选择模型、不运行 agent loop，
也不替代应用自身的授权。

本手册描述当前存在的 Go binding。公开语法遵循 Go 原生习惯；可观察的披露和 gateway
行为受 Contexture specification 约束。

## 1. 创建项目

使用 Go 1.25 或更新版本。原生 `project` 模板会创建一个静态 application declaration、一个
只读 Tool 和本地命令工作流：

```bash
contexture new operations --template project
cd operations
go mod tidy
go run ./cmd/assistant check
go run ./cmd/assistant list
go run ./cmd/assistant inspect --all --summary
go run ./cmd/assistant call operations/ping --input '{"target":"local"}'
```

`new` 会拒绝覆盖已存在的目录，并且只接受显式的 `project` 模板。它刻意保持很小：只增加你的
application 实际拥有的能力。

## 2. 声明一个 application

生成的 `cmd/assistant/main.go` 静态声明 application。该 declaration 是惰性的：它不会打开
Channels、启动 MCP server，也不会选择 Host transport。

```go
package main

import (
	"context"
	"log"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type statusInput struct {
	Service string `json:"service"`
}

func main() {
	status, err := contexture.NewTool(
		"status", "Return one service status.", true,
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
				Name: "operations", Description: "Operate services.",
				Instructions: "Inspect evidence before changing anything.",
				Skills: []contexture.Factory{func() contexture.Node {
					return &contexture.Skill{
						Name: "diagnose", Description: "Diagnose an unhealthy service.",
						Instructions: "Read status, then explain the evidence.",
						Uses: []string{"operations/status"},
					}
				}},
				Tools: []contexture.Factory{func() contexture.Node { return status }},
			}
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	_ = application
}
```

`Contexture(declaration)` 是 `DeclareApplication(declaration)` 的具名公开别名。两者都会保留
惰性 factory 并规范化 application 名称。每个 Role、Skill、Tool 都应通过 factory 声明；编译会
从这些 factory 创建新的不可变 graph snapshot。

## 3. 选择正确的节点

| 使用 | 适用情形 |
| --- | --- |
| Role | 一个职责边界，或多个明确分支之间的选择。 |
| Skill | 面向模型的流程、顺序规则或证据要求。 |
| Tool | Contexture 校验并调用的确定性 application code。 |

不要只为整理文件而增加 child Role。模型打开一个 Role 时会同时得到其全部直接成员，所以同一职责
所需的 Skill 与 Tool 通常应该留在同一个 Role 下。`Uses` ref 用于声明 Skill 需要的 Tool；应从
`list` 或已披露的卡片取得规范 ref，而不是凭记忆拼接。

## 4. 启动 Host 前先在本地工作

| 问题 | 命令 |
| --- | --- |
| declaration 能否编译？ | `go run ./cmd/assistant check` |
| 存在哪些 ref？ | `go run ./cmd/assistant list` |
| Agent 会收到什么？ | `go run ./cmd/assistant inspect --all --summary` |
| 一个只读 Tool 返回什么？ | `go run ./cmd/assistant call REF --input JSON` |

`check` 会编译但不会打开 application Channels。`call` 使用和 serving 相同的、已经校验的
Tool Binding。默认只允许 read-only Tool；writing Tool 必须显式传入 `--allow-write`。

## 5. 检查 Agent 可见 context

`inspect` 是 transport-free replay，而不是近似模拟。它构建的 instructions、discovery payload、
open card 和恢复文本，都来自 native implementation 所用的同一实现。

```bash
go run ./cmd/assistant inspect operations --all --summary
go run ./cmd/assistant inspect operations/runbook --read
go run ./cmd/assistant inspect --all --json > contexture-trace.json
```

`--all` 会按 Role 的广度优先顺序逐一访问每个可见 ref。`--summary` 保留 token estimate 和
Host-limit finding、隐藏 payload body。`--json` 适合 CI diff。`--read` 只运行无参数、只读的
内容 Tool，因此仅在确实需要本地读取时使用。在 project 外执行时，`contexture inspect` 会刻意
重放内置 demo，并把提示写到 stderr，以保证 JSON stdout 仍然有效。

## 6. 通过 MCP Host 提供服务

服务时不改变 declaration。server adapter 提供四个固定的 Contexture gateway Tool；业务 Tool
不会注册为 MCP 顶层 Tool，而是被渐进披露在 gateway 后面。

```bash
go run ./cmd/assistant serve
go run ./cmd/contexture demo --transport streamable-http
```

stdio 是默认 transport。只有在明确配置 Host 与网络时才使用 `--transport streamable-http`。
非 loopback 启动需要对应的 Host、origin 和 anonymous-access 决策；应处理 server option error，
而不是放宽这些限制。

Claude Code、Cursor 和 Codex 配置请使用 `server.Launch`。它从 server command 渲染 Host
configuration，而不是复制 application 已声明的 context。

## 7. 保持合同真实

提出改动前运行完整 repository gate：

```bash
go run ./internal/conformancecheck
go test -race ./...
go vet ./...
```

不要为了让 binding 通过而修改 golden output：这些文件是跨语言 protocol contract。

请阅读 [architecture.md](architecture.md) 了解依赖边界，阅读
[CONTRIBUTING.md](../CONTRIBUTING.md) 了解贡献规则，阅读 [RELEASING.md](../RELEASING.md)
了解刻意保持关闭的发布流程。只有全部 product-parity 和 release gate 确实满足后，模块才会发布。
