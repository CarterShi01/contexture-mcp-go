# Contexture Go 实现

[English](README.md)

Contexture 的 Go 实现。Contexture 是一个面向 MCP 应用的渐进披露框架，目标是
在能力持续增长时仍保持上下文可导航。

语言实现：
[Python](https://github.com/CarterShi01/contexture-mcp) ·
[TypeScript](https://github.com/CarterShi01/contexture-mcp-typescript) ·
[Go](https://github.com/CarterShi01/contexture-mcp-go) ·
[跨语言规范](https://github.com/CarterShi01/contexture-mcp/tree/master/spec)

> **当前状态：工程骨架，尚未发布。** 目前已经固定永久 module path，并建立
> Go 原生声明边界、依赖分层、CI 和规范版本锁定，但还不能替代 Python 参考实现，
> 也不应创建公开版本 tag。

## 架构边界

业务声明和 Host 适配器保持分离：

```text
应用声明
   ↓
不依赖 MCP SDK 的根包与内部编译器
   ↓
编译 → 披露 → 调用
   ↓
MCP 与可选 HTTP 表面
```

根包不得导入 MCP SDK。`server` 是适配层边界；当前只验证官方 MCP Go SDK
可以被正确集成，并不声称已经实现 Contexture 固定网关。

## 本地开发

需要 Go 1.25 或更新版本，以及 Git。

```bash
git clone https://github.com/CarterShi01/contexture-mcp-go.git
cd contexture-mcp-go
go mod download
go test -race ./...
go vet ./...
```

声明应用不会执行 root factory。编译、Index、披露和调用仍属于后续里程碑。

## 一致性状态

本实现锁定 Contexture Specification 0.12，具体提交记录在
[`conformance/specification.json`](conformance/specification.json)。只有通过共同
fixture 和 golden 输出的行为才算实现，不能用复制文档代替验证。

英文是项目第一语言，也是发生歧义时的权威文本。源代码注释、标识符、错误信息、
API 文档和发布说明默认使用英文；简体中文文档作为用户翻译持续维护。

## 许可证

Apache-2.0，参见 [LICENSE](LICENSE)。
