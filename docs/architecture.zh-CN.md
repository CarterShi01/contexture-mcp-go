# 架构

[English](architecture.md)

此 Go binding 使用 package 与 `context.Context` 表达 Python 参考实现的产品边界，尚未达到完整产品等价。

## 依赖方向

```text
根声明 facade → core/model → core/foundation
                         ↑
             core/mcpinterface
                         ↑
server MCP adapter ── server/surface     web HTTP adapter
```

SDK-neutral 层负责声明校验、规范 ref、不可变 Index、root-selected view、disclosure、execution binding 和 lifecycle；它们不能导入 MCP、HTTP、CLI 或框架相关 package。

`core/model` 是 Python lazy `contexture.core` facade 的原生等价物，root package 则 re-export
其公开 authoring 概念。Go 在编译期解析 package symbol，而不是首次访问时解析 attribute；导入任一
SDK-neutral package 都不会加载 Host adapter。

`server` 使用官方 MCP Go SDK，`surface` 投影 Prompt 和 Resource，独立 `web` package 将显式 route allowlist 映射到 `net/http`。业务 Tool 不会成为顶层 MCP Tool；Contexture 只暴露固定导航与调用 gateway。

## 已实现区域

1. core 节点模型、注册、校验、不可变 Index 与 disclosure；
2. 强类型 Tool binding、`context.Context`、固定 MCP gateway、Prompt、Resource 和 REST；
3. Channels、principal、telemetry、HTTP bearer identity、经过认证的请求级 root selection；
4. 原生 CLI、项目生成/发现、inspection、demo 和 stdio/streamable-HTTP launcher；
5. 外部 Go module 消费者与竞态测试。

所有适用的 0.14 源码与行为测试条目现在都具备定向原生证据；维护中的英文与简体中文产品文档以及
真实 Claude Code Host 验证也已记录。module metadata 与干净检出环境的 release audit 完成前，发布仍会
刻意保持关闭；Codex 另因本机账号未登录而阻塞。
