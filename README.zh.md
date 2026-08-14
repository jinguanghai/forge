# 铸剑炉 (Forge) — 通用数字智能体

> *The forge that shapes software.* 单一工具 · 十八道门 · 自我进化

**铸剑炉**是一个自托管的 AI 智能体引擎，基于一个激进的设计：**唯一工具** `forge`（多语言编译器沙箱，18 种 Gate）+ LLM 编排层。生成与执行严格分离——LLM 大脑写代码，铸剑炉执行，结果回传。无隐藏工具，无魔法。

## 核心特性

- **单工具架构**：一切能力经由唯一 `forge` 工具，生成的代码即时编译、执行、销毁
- **18 种 Gate**：python / go / sh / node / deno / rust / tcc / math / logic / system / knowledge / regex / chain / eprover / repair / self / tcm / browser
- **LLM 大脑 + 代码身体**：大脑实时推理，身体保持确定性；`repair`/`self` 门让智能体改进自身源码
- **纵深防御**：完整性守卫、审查门、基线审计（`defense_system/`）
- **稳健记忆**：原子写入（tmp+rename）、折叠/召回引擎、事件日志
- **流式 LLM 客户端**：三档推理强度、智能路由、`.env` 配置
- **跨平台**：Windows 一等公民；平台差异由独立文件隔离

## 快速开始

```bash
git clone https://github.com/jinguanghai/forge.git
cd forge
cp .env.example .env      # 填入 DEEPSEEK_API_KEY
go build -o forge .
./forge
```

## 安全与隐私

- 本仓库仅含**引擎源码**——不含任何密钥、个人数据或私有知识库
- 私有数据（个人知识、医案、古籍数据等）**永不**进入本仓库

## License

[MIT](LICENSE) © 2026 jinguanghai
