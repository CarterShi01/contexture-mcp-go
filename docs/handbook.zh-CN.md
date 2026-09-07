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

根 `contexture` package 是 SDK-neutral 的 declaration facade。其原生 authoring inventory 包括
`ApplicationDeclaration`、`Factory`、`Role`、`Skill`、`Tool`、`Channels`、`Principal`、`Prompt`
和 `Resource`。`Prompt` 与 `Resource` 是数据 declaration（分别是保留的 `PromptDeclaration` 和
`ResourceDeclaration` 写法的 alias），不是 Python 风格的 subclass base。应使用这些值填充
`ApplicationDeclaration` 的 `Prompts` 与 `Resources`。Go 通过 `ErrInvalidDeclaration`、
`ErrDuplicate`、`ErrInvalidInput` 等 error sentinel 报告 declaration 与 invocation 类别，并用
`errors.Is` 判断，而不是使用 Python exception class。`contexture.Version` 是 binding package version，
与 `contexture.SpecificationVersion` 不同。

Go 不模拟 Python exception inheritance，而是通过 `errors.Is` 提供同样有用的 category：
`ErrContexture` 是 package-wide 的 `ContextureError` 等价物；`ErrModelValidation`、
`ErrDeclaration` 与 `ErrDuplicateName` 对应各个 validation subclass。既有的
`ErrInvalidDeclaration` 与 `ErrDuplicate` 仍是精确的 Go spelling，同时也能归类到这些 parent。
`NodeNotFoundError` 保留有类型的 lookup fact，并提供 `Within`、`KnownRefs` 与
`DeveloperSummary`；其 summary 面向 developer，刻意不包含 agent recovery prose。直接的
`WrongDoorError` 同样只陈述 Tool fact；由 `Gateway` 将它转换为面向 agent 的下一步操作语句。

`contexture.PackageName` 是 framework metadata（`"contexture"`），绝不是 application 的 MCP identity：
Host 仍发布所声明的 application name。`contexture.ReferenceSeparator` 是 reference segment 之间规范的
`"/"`。四个面向 model 的固定名称是有类型的 `GatewayName` value：`DiscoverGatewayName`、
`OpenGatewayName`、`InvokeReadOnlyGatewayName` 与 `InvokeGatewayName`。它们由 model 与 MCP primitive
layer 共用同一个 foundation vocabulary。JSON-ready card 和 schema 使用 Go 原生的 `map[string]any`/`[]any`；
Contexture 有意不为 Python 中仅用于 static typing 的 recursive JSON type 或未使用的 `RequestId` annotation
暴露一个没有约束力的 `any` alias。

`Prompt`、`Resource` 与 `ModelOpenPolicy` 同样是由 foundation 拥有的 shared declaration data。保留的
`mcpinterface` 写法是兼容性 type alias，因此 application model 无需为了验证 publication 而依赖 MCP
primitive package。

`Principal` 是不可变的 request fact，而不是 authorization policy。它的 accessor 会为 scope 和 claim 返回
defensive copy。普通 Go diagnostic formatting（`%v`、`%+v` 与 `%#v`）只包含 subject、client ID、issuer
和排序后的 scope；claim 会被刻意脱敏，因为其中可能有 decoded token 或其他 sensitive value。

`Prompt.ModelOpen` 使用一个对 Go zero value 安全的 policy，而不是会意外保留全部 Prompt target 的
boolean。默认值 `contexture.ModelMayOpen` 同时允许 model navigation 与具名 person Prompt。设置
`ModelOpen: contexture.ModelReservedForPerson` 后，target card 仍会在其 parent 中可见，但只拒绝 model
的直接 open；具名 Prompt 与 `goto` 仍可为 person 打开它。无需编译 graph 即可判断的 Prompt 与 Resource
事实（空白 `Opens`、已提供但空白的 `Name`、description、URI 或无效 policy）会由
`DeclareApplication` 拒绝；target 是否存在以及 Resource Tool 的 shape 则在 publication 编译时检查。

`Index.Find` 与 `Index.Tool` 在 canonical lookup 失败时返回有类型的
`*contexture.NodeNotFoundError`。使用 `errors.Is(err, contexture.ErrNodeNotFound)` 和 `errors.As`
读取其 `Reason`、`Ref`、segment、scope、kind、wanted kind 与已知替代项。它是 Python
`NodeNotFoundError` 的 Go 等价物；`NoSuchMember`、`WrongKind` 等 `LookupFailure` constant 使这些
事实可由程序检查，而不附带 Host 专属 prose。
如果直接 `Runtime` 调用走错 mutation door，`errors.As` 也可读取
`*contexture.WrongDoorError` 的 `Ref` 与 `ReadOnly` facts；它仍会 unwrap 到 `ErrWrongDoor`。
Gateway caller 仍会收到原有的、带 agent 下一步操作说明的 `RefusedError`，并保留该 typed cause。

该 facade 有意不导入 MCP SDK、`server` 或 `web`。只有在 declaration 准备好被编译到某个 Host
surface 时，才导入 `server` 或 `web`。

### Typed Tool binding 与 explicit schema

`NewTool` 从带 tag 的 input struct 推导 schema。`NewToolWithSchema` 是用于 enum 等 reflection
无法直接表达约束的公开 escape hatch，但它不能让已发布的 card 承诺 Go 会拒绝的输入。构造时，其 explicit
root schema 必须是 object；properties 必须与 input struct 中 exported 且带 JSON tag 的 field 精确一致；
required name 必须与未使用 `omitempty` 或 `omitzero` 的 field 精确一致；并且必须设置
`additionalProperties: false`。这与 binding 的严格 `encoding/json` decoder 一致，在披露的 explicit schema
中保留该 unknown-field policy，并会在 Application compile 前以 `ErrInvalidDeclaration` 拒绝 drift。

reference-derived 的 `NewTool` card 有意省略 `additionalProperties`，因此它和 decoder 都会接受 unknown
argument，这与 pinned Python/MCP binding 一致；required 与 typed field 仍会被校验。若 contract 需要严格拒绝
unknown field，应选择 `NewToolWithSchema`；此时 explicit 的 `additionalProperties: false` 会被发布，并递归地
对 nested input struct 生效。

JSON tag option 会被完整扫描，而不是按位置解释，因此 `json:"value,omitempty,string"` 与
`json:"value,string,omitempty"` 都会让 `value` 成为 optional。property-level JSON Schema constraint 可以
收窄可接受的 value，但不能重塑 top-level object 或削弱 unknown-field policy。调用失败仍是
`ErrInvalidInput`，且绝不会进入 handler。explicit numeric field 必须声明位于目标 Go type width 内的有限
`minimum` 与 `maximum`；无界 JSON number 可能承诺 `encoding/json` 无法存储的 value。array item schema、map
value schema 与 map `patternProperties` 也会递归检查。struct field 不可使用 `patternProperties`，因为 strict
decoder 会拒绝每一个匹配的 unknown name。

两个公开 Tool constructor 都会立即以 `ErrInvalidDeclaration` 拒绝空 name、name 中的 `/` 或空 description；
手写的 `Tool` literal 会在 registration 或 compilation 时得到相同校验。constructor 要求明确给出 `readOnly`
bool，而原始 Go `Tool` literal 在编译前采用 Go 的 zero value（`false`，即 writing）。这是 Python 可选
`read_only=False` field 的原生等价物：正常 executable-constructor callsite 的 mutation classification 保持显式。
没有 Binding 的 Tool 只可存在于 disclosure-only Index；`Tool.Binding` 以及 runtime compilation 会将试图执行它的
行为分类为 `ErrInvalidDeclaration`。Binding schema 是 defensive copy，因此 caller mutation 不会改变之后的
`Schema` 结果或已编译 Tool card。

### Imperative registration

`ApplicationDeclaration` 是正常的 Go composition root：它会把 `Factory` 保持为惰性，直到
`Compile` 才调用。对于希望在声明 Application 前就于 registration 阶段构造并校验固定 forest 的程序，
`NewControllerManager` 是独立且有意提供的 imperative 选择。应使用带类型的 `RegisterRole`、
`RegisterSkill` 和 `RegisterTool`；它们的 typed factory argument 会在调用位置明确 standalone root 的
kind。`RegisterRoot` 仅适用于确实要在 runtime 才知道 kind 的调用者。
Go registration 接受 factory，而不是 Python 的 class-or-instance union：如有需要，可用 typed factory
包装已有 declaration。这样会明确 construction boundary，并让 manager 通过 snapshot 取得所有权，而不是
暴露可变的已注册 node。

manager 拥有防御性的 registration snapshot。root name 共用一个 namespace；暴露 root 时顺序为 Role、
standalone Skill、standalone Tool（每个 kind 内保留 registration order）。重复 identity、cycle、错误的
member group 以及无效 node fact 都会在 registration 时以 typed error sentinel 拒绝。
`manager.Application(name)` 和 `manager.Compile(name)` 会再次 snapshot 已注册的 forest；之后的调用者修改或
registration 都无法改变已有 Application 或 Index。

`NewControllerManagerWithChannels` 会把生命周期安全的 `Channels` interface 附给 manager 生成的
Application。`RebindChannels` 只影响之后生成的 Application/Index snapshot，绝不影响已经 compile 或正在
serving 的 server。这有意区别于 Python 会把任意 handle stamp 到每个 node 的做法：Go 将 dependency 保留在
compiled Index，绝不将它作为 model-node field 暴露。

`WithChannels` 是 Runtime 与 Host serving loop 使用的 transport-neutral lifecycle boundary。成功 scope 的顺序是
`Open → serve → Close → 已注册 cleanup 的逆序`；Open failure 不会调用 `Close`，但仍会回收失败前已经注册的
每一个 cleanup。`CleanupRegistrar.Defer` 只在 `Channels.Open` 运行时有效；若保留 registrar 并在之后注册，
会 panic。若 Open 或 serve panic，Contexture 会完成适用的 unwind，并且即使 Close 或 cleanup 也 panic，仍会
re-panic 原始 value。普通返回的 error 则保持原有 joined-error 行为。

### Compiled Index 查询

`Index` 是不可变的 compilation snapshot。`Count`、`Has`、`Bound` 和 `Channels` 报告其 capture
的 facts；`OfKind`、`NodesWithRefs`、`Skills`、`RolesWithRefs` 与 `RolesByLevel` 按已声明的
canonical order 返回防御性 node snapshot（最后一项是 breadth-first）。`Walk` 保持只返回 ref 的
兼容遍历；`NodesWithRefs` 是 Go 原生的 address/node pair 形式。`BindingOf` 与 `SchemaOf` 只可用于
bound Index；disclosure-only Index 会返回带 `ErrInvalidDeclaration` 类型的 error，不会暴露 execution
data，schema 也是防御性 copy。

`MatchingRefs` 依次按完整 prefix、最后 segment prefix、任一 segment prefix 和 substring 匹配，再按
rank、Unicode rune length 与 lexical order 排序；返回的 total 是截断前数量。Go 有意将负 limit 视为零结果（而非
Python 的 negative slice 语义），避免受限 completion response 被意外扩大。`Signpost` 只暴露 ancestor
ref 与 sub-role count；`Crossings` 列出跨越 root 的声明 `Uses` edge。二者都是结构性 Index facts，均不
披露 node member card。`Find` 与 `Signpost` 会在成功 lookup 前把重复、开头或结尾的 `/` separator 归一化为
同一 canonical address。

### Request root projections

`RootSelection` 要么是 `AllRoots()`，要么是用 `OnlyRoots` 创建的完整 root 精确 allowlist。它会拒绝
空 ref 和 descendant ref，在 resolve name 时不会泄露无关 root，并且只能通过 `Intersect` 收窄。
`RootSelectionError` 通过 `errors.Is(err, contexture.ErrInvalidSelection)` 分类无效或矛盾的 selector；
有效但位于当前 request surface 之外的 ref 则返回 `*RootOutsideSelectionError`，它会 unwrap 到
`ErrRootOutsideSelection` 并保留 `Ref`。

`NewSelectedGraph(index, selection)` 提供 request-safe graph view：`Roots`、`Walk`、`NodesWithRefs`、
`Find`、`RefOf`、`ParentOf`、`ChildrenOf`、`UsesOf`、`DependentsOf` 和 `MatchingRefs` 都会保留 canonical
ordering，同时排除其他 root。跨 root 的 `uses` 与 dependent 会被过滤而不是披露。`CurrentGraph(ctx)` 与
`CurrentSelection(ctx)` 仅在 Tool invocation 内有效。Go 有意让 invocation 外的 `CurrentGraph` 返回 `nil`
（不存在 ambient graph），而 `CurrentSelection` 会安全地默认为 all roots。Tool handler 可在整个 invocation
内保留并查询其唯一的 `CurrentGraph`；并发 call 会得到彼此独立的 root-projected graph。
`HeaderRootSelector` 仅将 `Contexture-Roots` 当作 attenuation request：它会验证未知 name 而不列出其他 root，
并与经过认证的 principal ceiling 求交。

每个已编译 `Node` 还公开 `Ref()`：它返回该 node 的 canonical address，而不从可变的 display field 重新拼接。
`BranchesOf` 与 `MembersOf` 是 Python base node traversal method 的 Go 原生等价物。Role 返回直接 child Role branch，
并按 Roles → Skills → Tools 的 declaration-group 顺序返回直接 member；Skill 与 Tool 因不持有 factory，未编译时也返回
空的 defensive result。Role containment query 和 `Role.Members` 一样都要求 compiled Index snapshot，且绝不会执行 factory。
未编译或 typed-nil node 上调用 `Ref()` 则返回 `ErrInvalidDeclaration`-typed error。这把 Python 已构造的 object member 映射到 Go
有意采用的 lazy factory，同时保持
canonical ref identity 与 compiled snapshot 的不可变性。

`Role.Branches`、`Role.Members` 与 `Role.Member` 为已编译 snapshot 提供相应的 Role-local
结构查询。Branches 仅返回直接 child Role；Members 按 declaration group 顺序返回直接 child Role、Skill
和 Tool；Member 在这三类直接成员之间按 name 查找，未找到时返回带 Role 已知 name（排序后）的
typed `NodeNotFoundError`。这些 API 返回 defensive snapshot，且绝不调用 lazy member factory。对于未编译的
Role，调用会返回 `ErrInvalidDeclaration`-typed error：这是一项有意的 Go 原生差异，因为 Go declaration
保存的是 factory，而 Python 保存的是已经构造的 member list。完整 forest 建好后会检查每个 `Uses` ref：它必须
可解析、唯一且非空，并且不得指向 node 自己的 ref。

### Telemetry

`ApplicationDeclaration.Telemetry` 可选地提供一个 usage collector；
`server.CompileApplication` 生成的 disclosure、gateway 和 Runtime 会共享它。若未提供，编译会创建
`MemoryTelemetry`。`NodeUsage` 是带类型、可直接 JSON 编码的 snapshot，包含 `ref`、`call_count`、
`error_count` 与 `last_used_at`；从未见过的 ref 保留该 ref 且计数为零。

框架只记录成功打开的 Role 与 Skill，以及实际执行的 Tool invocation（包括失败的 invocation）。
`discover` 和打开 Tool card 都不算一次 use。`CurrentTelemetry(ctx)` 只会在 Tool handler 的 request
context 内非 nil。exporter 的 error 或 panic 会被忽略，因此 telemetry 不会改变业务结果。
`MemoryTelemetry.Events()` 返回非破坏性的 snapshot，并有意保留全部 event；需要有界保留或远程导出时，
应提供自定义 `Telemetry` 实现。
`ReportTelemetry(telemetry, ref, failed)` 是供拥有额外 observation 的 Host boundary 使用的、公开的 Go
原生 Python `telemetry.report` 等价物；它同样隔离 exporter 的 error 与 panic。框架 navigation 和
invocation 会自动记录，无需调用者手动使用该函数。

## 3. 选择正确的节点

| 使用 | 适用情形 |
| --- | --- |
| Role | 一个职责边界，或多个明确分支之间的选择。 |
| Skill | 面向模型的流程、顺序规则或证据要求。 |
| Tool | Contexture 校验并调用的确定性 application code。 |

不要只为整理文件而增加 child Role。模型打开一个 Role 时会同时得到其全部直接成员，所以同一职责
所需的 Skill 与 Tool 通常应该留在同一个 Role 下。`Uses` ref 用于声明 dependency 而不是 containment，
并且可跨 root。打开 Role、Skill 或 Tool 时，会按 declaration order 将其 `Uses` target 投影为 route card；root
selection 会过滤被排除的 target，而不会泄漏它们。disclosure-only Index 仅生成结构性的 Tool card：它会省略
`input_schema` 和 `read_only`，因为它没有 executable binding。应从 `list` 或已披露的卡片取得 canonical ref，
而不是凭记忆拼接。

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

## 6. 提供由人控制的导航

Prompt 面向在 Host 菜单中做选择的人，而不是模型的第二个 surface。一个声明的
`PromptDeclaration` 打开一个固定 ref；每个已服务的 application 还会发布 `goto`，它所需的
`ref` 参数让人无需先要求模型导航就能浏览已知路径。

```go
application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
	// Roots: ...,
	Prompts: []contexture.PromptDeclaration{{
		Name:         "open-change-window",
		Opens:        "operations/change-window",
		Description:  "Open the change-window procedure.",
		ModelMayOpen: false,
	}},
})
```

具名 Prompt 与 `goto` 都使用同一条由人控制的打开路径。其文本会说明 ref、提供但不披露内容的
ancestor signpost，最后展示正常 node payload。`ModelMayOpen: false` 会把已声明 capability 保留在
模型导航之外；它不会对拥有 Host 的人隐藏该 capability。不要把 Prompt 当成 business Tool，也不要在
description 中重复其 procedure。

原生 MCP completion endpoint 只服务 `goto` 的 `ref` 参数，并且只返回当前 selected root surface 内的
ref。它最多返回 100 个值；若还有更多匹配，最后一个可见值会说明剩余数量，而 response 仍保留真实的
`total` 与 `hasMore`。针对其他 Prompt 或参数的 completion request 不会返回任何 Contexture ref。

## 7. 发布可由 Host 读取的文档

`ResourceDeclaration` 为树中已存在的 Tool 内容提供稳定 URI。它必须指向一个无参数、只读的 Tool，
因此 resource read 使用的仍是与本地只读 call 相同的已验证 Binding，也就不可能修改外部世界。Host
列出的是 resource metadata，读取的是 URI。

```go
application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
	// Roots: ... including a read-only operations/runbook Tool with no input.
	Resources: []contexture.ResourceDeclaration{{
		Opens:       "operations/runbook",
		URI:         "contexture://operations/runbook",
		Description: "The current operations runbook.",
		MIMEType:    "text/markdown",
	}},
})
```

位于 Host selected root surface 外的 Resource 既不会被列出，也不可读取。不要用 Resource 实现带参数的
查询、写操作，或再实现一次 Tool；这类能力应当通过 Contexture gateway 使用已声明的 Tool。

## 8. 通过 MCP Host 提供服务

服务时不改变 declaration。server adapter 提供四个固定的 Contexture gateway Tool；业务 Tool
不会注册为 MCP 顶层 Tool，而是被渐进披露在 gateway 后面。

`GatewayTools()` 暴露该不可变且有序的 inventory：`contexture_discover`、
`contexture_open`、`contexture_invoke_read_only` 与 `contexture_invoke`。
导航专用 Host 使用前两个的 `DisclosureGatewayTools()`；两个调用入口由
`ExecutionGatewayTools()` 提供。它们是 framework control，而不是业务 `Tool` node。gateway
查找或调用错入口时会返回带有 agent 下一步操作说明的 `RefusedError`；其 cause 仍保留供 Host
使用的 `NodeNotFoundError` facts。超出 selected root ceiling 的 ref 则刻意不同：它保持类型化
`RootOutsideSelectionError`，不会被改写成恢复建议，也不会透露被排除的 root。为 person 保留的
Prompt target 也会先检查同一 ceiling；随后只提示 agent 请用户运行 Host command，而不要绕过它。

```bash
go run ./cmd/assistant serve
go run ./cmd/contexture demo --transport streamable-http
```

stdio 是默认 transport。只有在明确配置 Host 与网络时才使用 `--transport streamable-http`。
非 loopback 启动需要对应的 Host、origin 和 anonymous-access 决策；应处理 server option error，
而不是放宽这些限制。

程序化启动时，`server.ContextureOptions{LogLevel: server.WarnLogLevel}` 控制
Contexture 生命周期日志。`server.ConfigureLogging` 会把同一结构化 logger 安装到 stderr，
所以 MCP stdio 始终独占 stdout。

除非设置 `ApplicationServerOptions.Instructions`，Contexture 会在 MCP 初始化响应中返回紧凑的
广度优先能力清单及固定导航合同。HTTP root selection 时，该清单按每个请求的 selected root
surface 生成，绝不会宣称被省略的 root。

Claude Code、Cursor 和 Codex 配置请使用 `server.Launch`。它从 server command 渲染 Host
configuration，而不是复制 application 已声明的 context。

## 9. 发布显式 REST surface

`web` 是一个独立的 `net/http` adapter，适用于 application 有意选择一小组 REST allowlist 的场景。
它绝不会从 Contexture graph 推导 public path。每条 route 都指向一个固定的 Tool ref：`GET` 与 `HEAD`
只能指向 read-only Tool，而 `POST`、`PUT`、`PATCH`、`DELETE` 只能指向 writing Tool。

```go
surface, err := web.NewRestSurface(runtime, []web.RestRoute{
	{Method: http.MethodGet, Path: "/status", Ref: "operations/status"},
	{Method: http.MethodPost, Path: "/restart", Ref: "operations/restart", Status: http.StatusAccepted},
}, web.RestRouterOptions{
	MaxBodyBytes: 1024 * 1024, // zero 使用相同的 1 MiB 默认值
	Authenticator: func(_ context.Context, request web.WebRequest) *contexture.Principal {
		if request.Headers["authorization"] != "Bearer expected" {
			return nil
		}
		return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "operator"})
	},
})
if err != nil {
	log.Fatal(err)
}

server := &http.Server{Addr: "127.0.0.1:8080", Handler: surface}
if err := surface.Serve(context.Background(), func(_ context.Context, _ http.Handler) error {
	return server.ListenAndServe()
}); err != nil && !errors.Is(err, http.ErrServerClosed) {
	log.Fatal(err)
}
```

只读参数来自 query string：单个值变为 JSON string，重复值变为 JSON array。命令接受空 body 或一个 JSON
object，并拒绝其他 media type、无效 JSON、非 object body 和超过配置上限的 body。`HEAD` 会回退到显式
`GET` route，保留其 response status 与 headers 但不返回 body。成功 response 是带
`Cache-Control: no-store` 的 JSON；失败 response 是结构化的 `application/problem+json`。可选的
authenticator 会收到 lower-case header 与重复 query value 的 snapshot。非空 principal 会放入 Tool 的
`context.Context`（`contexture.CurrentPrincipal`），同一 HTTP snapshot 可通过 `web.CurrentRequest` 获取。

Go adapter 会在构造时拒绝 1xx、204、205 与 304 route status：与 ASGI reference surface 不同，
`net/http` 无法在这些无 body status 下忠实输出 Contexture 所要求的 JSON representation。应使用普通的
最终 JSON status，例如 200 或 202。Tool 可以显式返回 `web.Reject("client-safe reason")`，从而产生
422 的 `rejected` problem；未预期的 Go error 仍会成为 500 的 `controller-failed` problem。

应使用 `surface.Serve` 包住实际 serving loop，这样 Contexture Channels 只打开一次，并在 loop 结束后关闭。
同一 surface 可以再次 serving，并建立一个新的 Channel lifetime。直接调用 `ServeHTTP` 适合测试，但不会
建立 application lifetime。

## 10. 保持合同真实

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
