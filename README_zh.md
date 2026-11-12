# <img src="web/public/favicon.svg" alt="openheron" width="72" height="72" /> openheron

**一个把「专家 Agent」当工具使的多 Agent 运行时**：每个专家 Agent 在配置里绑定适合自己的
大模型，父 Agent 只做编排与合成 —— token 花在真正需要它的地方。

- **专家配专模** —— 每个 agent 在 `agents/*.md` 里声明自己的 `model:`，简单检索不为不需要的
  推理能力付费。
- **工具即隔离** —— 专家 Agent 作为**有界能力**被调用：它自己的工具循环与中间推理留在它
  的上下文里，父 Agent 只拿到它写的那份结构化总结。
- **结构即节省** —— 省 token 不是靠提示词压缩，是靠边界让不该跨界的内容不跨界。

「谁能干什么」变成几条硬规则：

- **工具权限是点名的，缺省是要问** —— 没人点名的工具先问一声；例外由提供工具的那个文件的
  `allows:` 点名，每个 agent 再说明自己能用其中哪些。
- **委派有深度上限** —— 入口 agent 是第 0 层，深度 `d` 的 agent 当且仅当 `d < max_depth`
  才能再生子 agent；拓扑超限**启动期就报错**，不静默截断。
- **一个会话能同时跑多个 agent** —— 互不依赖的活并行分派。
- **配置是文件** —— 模型档案、提示词、工具档位都是能读能改的文件。
- **一个二进制，扔哪都能跑** —— 前端嵌进单文件，跨平台静态编译。

★ 边界先说清楚：省 token 在**可分解、工具输出冗长**的任务上最显著 —— 顺序依赖强的任务，
单 Agent 反而可能更快更准；「高性能」指**单位 token 的有效产出**，不承诺零延迟或无限并发。

> English version: [README.md](README.md)

## 文档

★ **其余的东西都在文档里，不在这里。** 这个文件说 openheron 是什么；文档说它怎么干活。

| 想做什么 | 看哪篇 |
|---|---|
| 装上它、跑起来、动手用界面 | [`docs/user-manual.md`](docs/user-manual.md) · [`docs/user-manual_zh.md`](docs/user-manual_zh.md) |
| 在这个树里干活 —— 分层、配方、测试 | [`docs/developer-manual.md`](docs/developer-manual.md) · [`docs/developer-manual_zh.md`](docs/developer-manual_zh.md) |
| 弄懂代码是怎么搭起来的 | [`docs/architecture.md`](docs/architecture.md) · [`docs/architecture_zh.md`](docs/architecture_zh.md) |
| 读配置系统与工具面 | [`docs/config-design.md`](docs/config-design.md) |
| 读运行时 —— run 生命周期、会话准入、上下文、日志 | [`docs/runtime-design.md`](docs/runtime-design.md) |
| 读工具从哪来、文件系统后端、MCP、沙箱 Kit | [`docs/tools-design.md`](docs/tools-design.md) |
| 读界面与它的资源 —— 附件与动效 | [`docs/ui-design.md`](docs/ui-design.md) |
| 读这棵树踩过的坑，以及它们的代价 | [`docs/pitfalls.md`](docs/pitfalls.md) |
| 把它打包成沙箱 workload | [`sandbox-kit/TUTORIAL.md`](sandbox-kit/TUTORIAL.md) · [`sandbox-kit/BUILDING.md`](sandbox-kit/BUILDING.md) |
| 遵守这里人与 AI 的约定 | [`AGENTS.md`](AGENTS.md) · [`AGENTS_zh.md`](AGENTS_zh.md) |
| 读出厂配置样例 | [`.openheron-example/README.md`](.openheron-example/README.md) |

★ 前三行是**双语**的 —— 英文是主版本，`_zh` 是中文版，两版一一对应。那五篇设计文档
**只有中文**：它们的中文内容接管了原文件名，所以上面的链接就是它们唯一的入口。
Kit 的那两份指南旁边各有一个 `_zh` 版本。

★ `docs/` 下更细的分册设计与行业对照在**上游仓库**里 —— 机制拆解与取舍见上游的
`docs/positioning.md`。
