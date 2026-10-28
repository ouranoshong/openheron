# openheron

**一个以「Agent 即工具」为原语的多 Agent 运行时**：专家 Agent 各自绑定适合自己的大模型，
父 Agent 只做编排与合成，token 花在真正需要它的地方。

配置定义专家与模型的映射，运行时在工具边界强制上下文隔离；单二进制常驻，热更新演进。

它靠三条机制拿到「省 token」：

- **专家配专模** —— 每个专家 Agent 在配置里绑定适合自己的模型：简单任务（检索、提取、摘要）
  不为不需要的推理能力付费，复杂任务也不将就小模型。
- **工具即隔离** —— 专家 Agent 作为一个**有界能力**被调用：它自己的工具循环与中间推理留在
  它的上下文里，父 Agent 只拿到它写的那份结构化总结。
- **结构即节省** —— 省 token 不是靠提示词压缩，是靠「Agent 即工具」的边界让不该跨界的内容不跨界。

它同时把「谁能干什么」变成几条硬规则：

- **工具边界分层，永不合并** —— 外层 `policy/permissions.yaml` 定全局 `ask`/`deny`，
  内层每个 agent 用自己的 `disabled_tools` 收窄。外层的 `deny` 对所有 agent 生效，
  **子 agent 放松不了它**。
- **委派有深度上限** —— 入口 agent 是第 0 层，深度 d 的 agent 当且仅当 `d < max_depth`
  才能再生子 agent；拓扑超限**启动期就报错**，不静默截断。
- **一个会话能同时跑多个 agent** —— 互不依赖的活并行分派；用量**按 agent 分账**。
- **配置是文件** —— 模型档案、提示词、审批策略都是能读能改的文件：要 review 就 review，
  要提交进版本库就提交，不管它也有 `openheron init` 的出厂配置可用。
- **一个二进制，扔哪都能跑** —— 前端嵌在 41MB 的单文件里，跨平台静态编译。

★ 边界先说清楚，比事后解释便宜：

- 隔离是**完全的**：专家的中间过程**不进父的任何一轮上下文** —— 父只拿到专家写的那份
  总结。而**可观测性同样是完全的**：专家的思考与工具调用实时出现在界面上（带归属），
  也留在记录里，事后能查「task 到底改了什么」。
- 省 token 在**可分解、工具输出冗长**的任务上最显著；顺序依赖强的任务，单 Agent
  反而可能更快更准。
- 「高性能」指**单位 token 的有效产出**，不承诺零延迟或无限并发 —— 嵌套调用天然有串行开销。
- 专家配专模要**配对才划算**：出厂配置里所有 agent 共用一个模型。配错模型比不配更浪费。

★ 本仓库是**代码面**：Go 后端 + React 前端 + 构建配置。设计文档（`docs/`）
不随本仓库分发 —— 机制拆解与行业对照见上游的 `docs/positioning.md`。

```bash
make build                       # 构建（前端 + 单文件二进制）
cd /your/project && openheron init   # 在你的项目里生成最小可运行配置
export DEEPSEEK_API_KEY=sk-...  # 密钥只从环境变量读
openheron                            # 启动，打开 http://localhost:8080
```

## 📖 文档

`docs/` **不随本仓库分发**（见上方说明）。下列设计文档在上游仓库里：

| 文档 | 内容 |
|---|---|
| `docs/user-manual.md` | **用户使用手册**（面向使用者） |
| `docs/developer-manual.md` | **开发者手册**（面向改这个仓库的人：分层、依赖铁律、配方、测试） |
| `docs/positioning.md` | **定位与取舍**：专家配专模 × Agent 即工具 —— 机制拆解与边界 |
| `docs/architecture.md` | 总览与分册索引 |
| `docs/config-design.md` | 配置 schema、分层、校验、热更新 |
| `docs/agent-design.md` | Agent 池、审批、深度保护 |
| `docs/multi-agent-design.md` | 多角色、并行委派、按 agent 分账 |
| `docs/api-v2-design.md` | `/v2` 接口族与 AG-UI 事件契约 |
| `docs/frontend-design.md` | React 前端架构与界面 |
| `docs/config-editor-design.md` | 配置编辑器（`/v2/settings` 六条路由 + 界面） |
| `docs/checkpoint-store.md` | Checkpoint Store：审批恢复的真相源 |
| `docs/kb-design.md` | 知识库 |
| `docs/init-design.md` | `openheron init` |
| `docs/llm-common.md` | 厂商协议层 |

本仓库自带的两份约定文档是 **[AGENTS.md](AGENTS.md)**（给在这儿工作的人与 AI 的
项目约定）与 **[.openheron-example/README.md](.openheron-example/README.md)**
（出厂配置样例的用法）。

## 开发

```bash
make build-go   # 只构建后端
make web-dev    # Vite dev server（:5173，代理到 :8080）
make dev        # Go 自动重建重启（air）
make check      # lint + test
```

给 AI 与协作者的约定见 [AGENTS.md](AGENTS.md)。
