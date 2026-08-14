# 铸剑炉 (Forge)

通用数字智能体 — 单工具驱动多语言编译器沙箱。

铸剑炉是一个自托管的 AI 智能体引擎：一个名为 `forge` 的工具（多语言编译器沙箱，18 种 Gate），
配合 LLM 编排层，即可完成编码、系统管理、文件处理、数据分析、网络操作与自动化等数字世界任务。

## 核心特性

- **单工具架构**：所有能力经由唯一工具 `forge`（编译执行、用完即销毁），生成与执行分离
- **18 种 Gate**：python / go / sh / node / deno / rust / tcc / math / logic / system /
  knowledge / regex / chain / eprover / repair / self / tcm / browser
- **中文原生**：面向中文用户的思维与输出，中医知识库专项支持（tcm gate）
- **自托管**：.env 配置，可接入 DeepSeek 等 LLM 提供商

## 快速开始

1. 克隆仓库并准备 Go 1.26+ 工具链
2. 复制 `.env.example` 为 `.env`，填入 LLM API Key
3. `go build` 编译主程序，运行即可

## 安全说明

- 本仓库仅包含引擎源码与工具实现，**不含任何密钥、个人数据或私有知识库**
- 私有数据（如个人知识库、医案、古籍数据等）永不进入本仓库
- 使用前请自行审查 `.env` 与运行时文件，确保不被提交

## License

[MIT](LICENSE) © 2026 jinguanghai
