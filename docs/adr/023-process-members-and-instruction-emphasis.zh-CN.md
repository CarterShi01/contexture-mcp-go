# ADR 023 — 过程成员与框架指令强调

**状态：** 已接受

**日期：** 2026-09-10

**取代：** ADR 021「Role Publication 与指令组合」。

## 背景

0.14 的 `Publication` 只表达了更一般机制的收尾一半：Role 可能需要为工作某个阶段提供一棵
单独披露的能力子树，并由框架要求 Agent 进入。准备阶段具有相同形状。旧版追加段落与普通业务
文字没有明显区别，也无法清晰表达两者的不同权威。

## 决策

无兼容别名地移除 `Publication` 与 `Role.Publication`。Go 暴露两个不同的强 Role 类型
`PreProcess` 与 `PostProcess`，以及匹配的惰性 `Role.PreProcess` / `Role.PostProcess` 槽。
函数签名拒绝普通 Role 和相反过程类型。两种过程类型都不能成为 application root，在线上仍为
kind `role`。

包含顺序严格为 pre-process、children、post-process、Skills、Tools；branches 与路由 roster
仍只包含 children。每棵过程子树完整遵守普通的唯一性、循环、canonical address、Channels、
Binding、selection、manager snapshot、Prompt audience 与 disclosure-only 规则。包含不会继承
过程成员；嵌套必须显式声明。

ACTIVE owner instructions 依次组合：可选的固定 PreProcess 框架合约、未经修改的业务
`Role.Instructions`、可选的固定 PostProcess 框架合约。每个合约使用稳定的首尾标记和
`>>> REQUIRED:` action 行，通过单引号引用 view 实际提供的 ref，并调用既有
`contexture_open`。指定成员也必须作为普通 Role card 出现；任一卡片不可用时，整个 open 会被
拒绝，而不是发出悬空或隐藏 ref。ROUTE 与 INSPECT 均不披露 designation 或 contract。

公开 `BindingInstruction(source, body, action)` 用于应用自己的硬规则。它大小写不敏感地拒绝
空 source 或以 `contexture` 开头的 source。框架 composer 保持私有，因为只有本包可以声明
框架权威。

## 后果

这是 pre-1.0 的破坏性 API 与 payload 变更。`TaskPublication` 等业务 subclass 名称可以保留，
但其基类必须改为 `PostProcess`，owner 槽必须改为 `PostProcess`。本决策不增加 node kind、
gateway、callback、开始/结束事件或 workflow runtime。打开过程 Role 只披露流程；只有显式
Tool 调用才产生副作用。写给 Agent 的流程不是可强制不变量，因此保证仍应放在能够拒绝违规调用
的 Tool 中。

Go 证据位于 `process_test.go`、`process_integration_test.go`、`public_api_test.go` 与外部 module
release consumer。规范源固定为 Python revision
`cda2721c7c40128cd0b7eef990e5909edabd3b17`，specification 0.16。
