# AGENTS.md

给在本仓库工作的人与 AI 的项目约定。

> ★ **本仓库是代码面**：Go 后端 + React 前端 + 构建配置。设计文档 `docs/`
> **不随本仓库分发** —— 下面凡引用 `docs/xxx.md` 的地方，说的都是上游仓库里
> 的那份文档。改语义前若拿不到它，先问，别凭印象改配置或接口。

## 语言约定（强制）

**所有运行时输出用英文**：日志打印、错误信息、警告信息、面向用户的消息提醒。

| 范围 | 规则 | 例子 |
|---|---|---|
| 日志 | 英文 | `obs.Logger().Info("[main] config updated", "version", v)` |
| 错误 | 英文 | `errors.New("query must not be empty")` |
| 警告 | 英文 | `fmt.Fprintln(os.Stderr, "[config] hot reload failed, keeping previous config:", err)` |
| 消息提醒 | 英文 | `c.deferKey("watch.debounce fell back to default: %s", msg)` |
| 接口响应体 | 英文 | `c.JSON(400, map[string]string{"error": "thread not found"})` |

**范围之外，以下三类保持中文，不要改**：

| 范围 | 理由 |
|---|---|
| **注释与文档注释** | 本仓库的 `docs/` 与代码注释一律中文，见 `docs/` 下的设计文档 |
| **给模型的提示词** | `inputctx` 构造的 context prompt 面向 LLM，已是英文；改动会改变 agent 行为 |
| **测试用例名与注释** | 描述性文本，不是运行时输出 |
| **界面文案** | 面向用户的字符串（按钮、占位符、状态提示、aria-label），与错误文案同理 |

判断标准：**这段字符串在进程运行时会被打印或返回给调用方吗？** 会 → 英文；不会 → 保持中文。

为什么：错误信息常被 grep、被日志采集、被 `llm/common` 的 `Kind` 分类消费；混语言会让排障时的检索与匹配失效，也让 `/v2/config` 的 `deferred` 字段混入两种语言。

**日志出口只有一个**：`obs.Logger()`（`openheron/agentkit/obs`）。业务包里不要再直接
`log.Printf` / `fmt.Fprintln(os.Stderr)` —— 级别（Info / Warn / Error）与前缀
只有一处定义，而级别决定了一行会不会出现在默认输出里。

例外只有一个：`llm/common` 用标准 logger —— 它是零 openheron 依赖的叶子包，
引 `obs` 就破了那条铁律（`docs/architecture.md` §3.2.1）。

## 其它约定

- **注释写「为什么」，不写「是什么」** —— 尤其是那些被刻意设计成反直觉的地方。
- **不要在 API 层复制业务装配逻辑** —— 需要读「某个 agent 的生效工具表」时，由 `agent.Builder` 暴露只读查询，不要重算一遍 `Table.Blocked` / `Policy.TierOf`。
- **归档优先于呈现** —— SSE sink 断裂后要停止渲染但继续消费事件流，否则 `Outcome.Intermediates` 不完整，历史会缺。
- **`deny` 优先于 `disabled_tools`** —— 外层策略是子 agent 无法放松的唯一保证。
- 改配置语义前先读上游的 `docs/config-design.md`；改接口前先读 `docs/api-design.md`；
  新增 `/v2` 接口读 `docs/api-v2-design.md`；动前端读 `docs/frontend-design.md`；
  动测试环境（端口 / 隔离目录）读 `docs/frontend-design.md` §11.2 与
  `docs/config-design.md` §5.2。**这些文档不在本仓库里** —— 拿不到就先问。

### 前端（`web/`）

- **不要把框架的语义写进注释当契约**。上一版前端的核心问题就是有一批
  「必须先读框架源码才敢改」的行为，它们全靠注释维持，一改就错。现在
  `lib/sse.ts` / `lib/events.ts` / `lib/store.ts` 三层都不依赖框架，正确性
  由 `bun test` 证明；**新增逻辑请落在这三层里**，让它们保持纯函数。
- **zustand 的 selector 必须返回引用稳定的值**。每次调用造新数组/新对象
  会让 `Object.is` 永远判定「变了」→ `Maximum update depth exceeded` 白屏。
  用模块级空常量，返回数组的 selector 配 `useShallow`。
- **zustand 的 selector 与组件里都不要写 `?? []`**。造新数组/新对象会让
  快照永远「变了」→ 无限重渲染 → 白屏。用模块级常量或 store 里的 selector。
- **界面改动必须配 Playwright spec**（理由与验收要求见下节「测试」）。

## 测试

### 端口：测试有自己的环境

**端口与路径全部定义在仓库根的 `.env.test`（已提交）**，Vite、Playwright、
冒烟脚本三处共读同一份。改端口只改这一个文件。

| | 开发 | 测试 |
|---|---|---|
| openheron 本体 | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E 假后端 | — | **:18180** |

### 命令

| 命令 | 覆盖 | 需要前置 |
|---|---|---|
| `make test` | Go 全量（`go test ./...`） | — |
| `cd web && bun run test:unit` | 纯函数层（reducer / 分帧 / store / 高亮） | — |
| `make test-e2e` | 真实浏览器 + 确定性假后端 | 无（自带假后端） |
| `make smoke` | 真后端联调：SSE 分帧、历史回放、审批接线 | **测试实例在跑** |
| `make lint` | Go vet + gofmt + oxlint + tsc | — |
| `make check` | `lint` + `test`，提交前跑这个 | — |

起测试实例（`make smoke` 的前置）：

```bash
make test-env-backend   # 前台起，:18099，独立配置 + 独立会话目录
make test-env-reset     # 只重建测试目录，不启动
make test-env-sync      # ★ 改过开发侧的 agent 定义后必须跑
```

单个冒烟脚本（默认就连 :18099，**不需要手写 `OPENHERON_BASE`**）：

```bash
cd web && bun run smoke:interop            # SSE 分帧、拼接、历史水合
cd web && bun run smoke:no-undefined-url   # 不会发出拼错的 URL
cd web && bun run smoke:hitl               # 审批 resume 走通
```

### 规则

- **改界面必须浏览器验证**。「我改了，测试也绿了」不构成验收依据 ——
  脚本级测试看不见渲染层，上一轮 10 个冒烟全绿而界面是坏的。
- **E2E 用假后端，不要改成真后端**。真后端依赖模型配合，失败时分不清是
  界面坏了还是模型没听话；`retries` 一开，失败变成噪音，三轮后没人看。
  与真实后端的协议层验证由 `make smoke` 负责，两者互补。
- **`smoke:hitl` 会抖，约 5 次里 1 次** —— 它依赖模型*选择*调 `write_file`
  才能触发审批。那是模型的自由，不是代码回归；脚本会重试，全都不触发时
  **明确报错**，不伪装成接线有问题。别用重试次数掩盖它。
- ★ **`smoke:hitl` 有一个前置条件，缺了它会 100% 失败（不是「抖」）**：
  默认 agent（main）的 `write_file` 必须**不在** `disabled_tools` 里 ——
  被禁用时模型根本调不出那个工具，自然没有审批可触发。
  2026-10-21 实测：本地 `.openheron/agents/main.md` 写着
  `disabled_tools: [write_file, edit_file, execute]`，于是脚本连试 8 次
  全是「模型没调 write_file」。验审批接线前先确认这条：
  `curl -s :18099/v2/agents | grep -A3 disabledTools`。
  ★ `.openheron/` 是**本地未跟踪**的配置，改它会影响你自己的真实 agent
  （放开 `write_file` 意味着那个 agent 真能写文件）—— 要改先问自己一句。
- **端口错开不等于数据隔离**。openheron 的会话写在 `.openheron/state/`，测试写
  进去就污染真实历史。测试实例把 `.openheron-test/project` 当项目根并**预置
  自己的 `.openheron/`**，让配置上溯停在那里 —— 否则上溯会找到仓库根那份。
  端口与状态必须**一起**隔离。
- **`make test-env-sync` 是必须的，不是可选的**。测试实例里的
  `agents/ llm/ policy/ skills/` 是从开发侧**复制**的，改完 agent 不
  同步，测的还是旧定义。
- **`pkill` 要用 `-x`，不要用 `-f "bin/openheron"`**。`-f` 匹配完整命令行，
  会把执行这条命令的 shell 自己也杀掉（它的命令行里就含这个字符串），
  表现为命令无响应、像是卡住了。正确写法：`pkill -x openheron`。
  ★ **`ps | grep | kill` 是同一种病**：它匹配的也是完整命令行，于是执行这条
    命令的 shell 同样会被杀（命令行里含那个搜索串）。要按名字杀就
    `pgrep -x <名字>`（只看 `comm`，不看命令行）。
  ★ 进程名超过 15 个字符时 `comm` 会被内核截断（`openheron-multi-root` 变成
    `openheron-multi-roo`），`-x` 就匹配不上了 —— 那时按端口找 PID
    （`ss -ltnp`）逐个 kill。
- **端口被占时让它响亮地失败**。`playwright.config.ts` 的
  `reuseExistingServer: false` 与 Vite 的 `strictPort: true` 都是刻意的：
  复用开发 vite 会让 E2E「全绿」但浏览器在跟真后端说话，
  `OPENHERON_API_TARGET` 根本没注入。**不要图省事改成 `!CI`**。
- **被强杀的 Playwright 会留下孤儿占端口**（webServer 子进程不随之退出）。
  再跑时报 `already used`，那不是配置坏了 —— 先清进程。
  ★ 表现是 `Error: http://127.0.0.1:18180/v2/health is already used`：
    端口被上一轮的 vite / 假后端占着，而 `reuseExistingServer: false` 是
    刻意的 —— 所以它**响亮地失败**（这正是要的）。清掉孤儿再跑。
  ★ 别在 E2E **跑着的时候**清进程：一条 `pkill -x vite` 会把当轮正在用的
    webServer 杀掉，而 Playwright 还在等它的健康检查 —— 表现为「端口一直没
    起来、测试卡在启动」。要清就先把测试停掉。
- **长任务要放到后台任务里跑**，不要用 shell 的 `&`。后台进程活不过那次
  工具调用（调用一结束它就被杀），表现是「跑了一会儿就没了」「端口始终
  没起来」—— 而日志里只有第一行，看不出是被谁杀的。
- **新增/删除用例要同步文档里的条数**。`docs/frontend-design.md` 的目录树
  把条数写死了，是为了让「忘了改」变成一次 diff 冲突而不是无声漂移 ——
  上一次的 65/14 已经漂了三个轮次没人发现。
- **动 `bun run build`、依赖或构建流程后要验新 clone**
  （`rm -rf node_modules && bun install` 再跑一遍）。本地能过不代表清单是
  对的：幽灵依赖（代码 import 了但 `package.json` 里没有）只有清空重装
  才暴露。`.gitignore` 的例外规则也有同类坑 —— git 逐层判断，父目录被
  排除就不再下降到子文件，近的规则优先。

隔离的完整细节见 `docs/frontend-design.md` §11.2 与
`docs/config-design.md` §5.2。

## 关联文档

★ 下列文档在**上游仓库**里，**不随本仓库分发**（本仓库只有代码面）。

| 文档 | 内容 |
|---|---|
| `docs/user-manual.md` | **用户使用手册** —— 安装、配置、界面操作（面向使用者，不含内部设计） |
| `docs/developer-manual.md` | **开发者手册** —— 分层、依赖铁律、开发配方、测试与排障 |
| `docs/implementation-audit.md` | **落地审计报告** —— 哪些没做、哪些是文档漂移（2026-10-23） |
| `docs/hygiene-research.md` | **代码卫生调研** —— 无出口的导出符号、第三方归档、死代码（2026-10-23） |
| `docs/agent-architecture-survey.md` | **外部 Agent 架构调研** —— pi / rakazo / OpenClaw / Hermes / Letta 的 gateway 与后台服务问题（2026-10-24） |
| `docs/positioning.md` | **定位与取舍** —— 专家配专模 × Agent 即工具：机制拆解与边界（2026-10-25） |
| `docs/architecture.md` | 总览与分册索引 |
| `docs/config-design.md` | 配置 schema、分层、校验、热更新 |
| `docs/api-design.md` | 旧的无前缀接口（**已于 2026-10-23 随 M3 下线**，保留为历史记录） |
| `docs/api-v2-design.md` | `/v2` 接口族与 AG-UI 事件契约 |
| `docs/frontend-design.md` | React 前端架构与界面 |
| `docs/agent-design.md` | Agent 池、审批、深度保护 |
| `docs/agent-design-v2.md` | agent 装配 API v2（`deep.Config` 请出 openheron） |
| `docs/multi-agent-design.md` | 多角色、并行委派、按 agent 分账与历史过滤 |
| `docs/api-v2-design.md` | `/v2` 接口族与 AG-UI 事件契约 |
| `docs/frontend-design.md` | React 前端架构与界面 |
| `docs/config-editor-design.md` | 配置编辑器（`/v2/settings` 六条路由 + 界面） |
| `docs/deep-config-fields.md` | `deep.Config` 字段使用参考 |
| `docs/checkpoint-store.md` | Checkpoint Store：审批恢复的真相源 |
| `docs/kb-design.md` | 知识库 |
| `docs/init-design.md` | `openheron init` —— 二级（项目级）配置的最小可运行初始化 |
| `docs/llm-common.md` | 厂商协议层 |