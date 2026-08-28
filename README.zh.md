# 铸剑炉 — 通用数字智能体

> 塑造软件之炉。一个工具，十二道 Gate，内核确定性。

**铸剑炉** 是一个自托管 AI Agent 引擎，基于一个刻意设计：**单一工具**（`forge`）负责编译、执行、销毁跨 **12 种语言与逻辑 Gate** 的代码，并配合 LLM 编排层。生成与执行严格分离 —— LLM 大脑写代码，铸剑炉执行，判定回灌。

核心哲学是**结构性的确定性**：LLM 是活的、统计的系统，实时推理且会漂移；身体是确定的程序，负责执行、验证、兜底。所有能力流经一个工具，无隐藏钩子，无魔法。

## 亮点

- **单工具架构** —— 所有能力经一个 `forge` 工具；生成的代码现场编译、执行、销毁
- **12 道 Gate** —— python / go / sh / node / math / logic / regex / knowledge / tcm / browser / chain / self
- **LLM 大脑 + 代码身体** —— Agent 实时推理，身体保持确定性；`chain` 编排多 Gate 管道，`self` 允许 Agent 改进自身源码（需审批）
- **纵深防御** —— 完整性守卫、评审 Gate、基线检查
- **健壮记忆** —— 原子存储（tmp+rename）、折叠/召回引擎、事件日志
- **流式 LLM 客户端** —— 三级 reasoning_effort、智能路由、`.env` provider 配置
- **跨平台** —— Windows 一等公民；平台文件隔离 OS 差异

## 快速开始

```bash
git clone https://github.com/jinguanghai/forge.git
cd forge
cp .env.example .env      # 设置 DEEPSEEK_API_KEY（任意 OpenAI 兼容 provider）
go build -o forge .
./forge
```

## 架构

身体是一小段 Go 程序：`agent.go` 编排 LLM 循环，`forge.go` 实现 Gate，`llm.go` 从 provider 流式读取，`config.go` 管理配置，`ux.go` 渲染终端。会话统计与展示助手各居其文件（`stats.go`、`style.go`）以分离职责。

## 许可证

MIT
