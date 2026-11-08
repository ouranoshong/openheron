# AGENTS_zh.md

给在本仓库工作的人与 AI 的项目约定。

本文件的英文版是 **[AGENTS.md](AGENTS.md)**；两版内容一一对应，改一处要改两处。


> ★ **本仓库是代码面**：Go 后端 + React 前端 + 构建配置。
>
> 本仓库自带**十一组**中英双语文档，随代码面一起维护
> （**英文是主版本，`_zh` 后缀是中文版**）：
> `README.md` / `README_zh.md`、
> `docs/architecture.md` / `docs/architecture_zh.md`（架构设计说明）、
> `docs/user-manual.md` / `docs/user-manual_zh.md`（用户使用说明）、
> `docs/developer-manual.md` / `docs/developer-manual_zh.md`（开发者说明）、
> `docs/context-usage-design.md` / `docs/context-usage-design_zh.md`（上下文使用预估设计方案）、
> `docs/session-concurrency-design.md` / `docs/session-concurrency-design_zh.md`（会话并发设计方案）、
> `docs/run-lifecycle-design.md` / `docs/run-lifecycle-design_zh.md`（run 生命周期设计方案）、
> `docs/fs-backend-design.md` / `docs/fs-backend-design_zh.md`（文件系统后端设计方案）、
> `docs/sandbox-kit-design.md` / `docs/sandbox-kit-design_zh.md`（Docker Sandbox Kit 打包设计方案）、
> `docs/mcp-design.md` / `docs/mcp-design_zh.md`（MCP 接入设计方案）、
> `docs/config-design.md` / `docs/config-design_zh.md`（配置系统设计说明）。
> **另有一篇只有中文的设计文档**：`docs/tool-surface-design.md`（工具面 ——
> 一个 agent 拿到哪些工具，以及配置怎么被管理；是一份**提案**）。
> **`docs/` 下其余设计文档仍在上游仓库** ——
> 下面凡引用 `docs/xxx.md` 而这几组之外、又不是上面那篇中文文档的，都是上游那份。
> 改语义前若拿不到它，先问，别凭印象改配置或接口。
>
> ★ **新写的架构与代码设计类文档只写中文。** 讲代码怎么搭起来的文档 —— 分层、
> 某个子系统的设计、一次重构方案 —— 没有第二个读者：读它的人读中文，而手工维护的
> 英文对照版是一份会漂移的第二拷贝。`docs/tool-surface-design.md` 是按这条规则写的
> 第一篇。
> ★ **仍然要双语的是给用户或集成方读的东西**：`README.md` 与 `docs/user-manual.md`。
> 不假定他们的读者读中文。
> ★ **这条规则不退休上面那十一组。** 它们已经存在，照旧维护；规则管的是从现在起
> 新写的文档。
>
> ★ 前面四组讲的是**代码面已经是什么样**；`docs/context-usage-design.md`
> 讲的是**上下文占用与压缩** —— 占用仪表盘（§2、§4、§5）、压缩及其持久化（§7）、
> 以及压缩标记的界面渲染（§7.7）都已在代码里。
> `docs/session-concurrency-design.md` 讲的是**会话并发** —— 一个会话一轮的准入闸门
> （§5）、跨客户端的 409（§5.2）、以及前端的忙态处理（§5.4-§5.6）都已在代码里。
> `docs/run-lifecycle-design.md` 是个例外：它是一份**提案**，不是描述。
> 它的 §1 讲的是代码现状 —— 今天一轮绑在一条 SSE 连接上 —— §2 往后讲的是
> 要建成什么样：run 变成一行持久事实、租约配 fence、连接降级为订阅者。
> **§2 往后的东西都还不在树上。**
> `docs/sandbox-kit-design.md` 也是一份**提案**：§1 讲 openheron 进程今天是什么样 ——
> 一个进程，一对 HTTP/SSE 服务，文件工具与 shell 工具共用同一个后端；
> §2 往后讲把**整个进程**打包成 Docker Sandbox Kit，于是边界往外挪而不是往里挪，
> 读路径也被罩住了。
> **§2 往后的东西都还不在树上。**
> `docs/fs-backend-design.md` 讲的是**文件系统后端** —— `eino-ext` local 后端的有界分支
> （§5）以及每个工具都要经过的那条收敛规则 —— 它**已实现**：`agentkit/fsx` 已在两处
> 调用点接上。
> `docs/mcp-design.md` 讲的是 **MCP 接入** —— `mcps/` 配置分区、
> `mcp__<server>__<tool>` 命名空间、住在 server 自己文件里的档位，以及 `/mcps` 视图。
> **它已实现**：分区、provider、管理器与端点都在树上。★ §8 —— 沙箱 Kit 怎么打包一个
> MCP server —— 仍然是提案。
> `docs/config-design.md` 讲的是**配置系统** —— 配置根与它的三级解析、各个分区与它们的
> 文件规格、加载与优先级、热更新，以及依赖铁律。**正文讲的是代码现状**；
> `mcps/` 分区（§6.7）与住在工具旁边的档位（§6.6）**已实施**。★ **全局 deny** 里的
> 前缀匹配（§6.3）跟着 deny 清单一起没了：「不被暴露」现在只由文件的 `allows:` 与 agent
> 的 `tools` 两处说。
>
> ★ 有一个目录不是文档组：**`sandbox-kit/`** 装着把 openheron 打包成沙箱 workload 的
> Docker Sandbox Kit —— `openheron/`（workload：描述文件、内容配方、入口脚本），
> 外加每个厂商一份 mixin（`openheron-deepseek/`、`openheron-openai/`：模型凭据与它的网络规则）。
> `sandbox-kit/TUTORIAL.md` 是给用户的指南 —— 跑已发布的镜像 ——
> `sandbox-kit/BUILDING.md` 是给开发者的 —— 构建、改描述文件、发布一个版本
> （中文版是 `TUTORIAL_zh.md` 与 `BUILDING_zh.md`），
> `sandbox-kit/acceptance/run.sh` 是验收跑批 —— 设计里的每条断言都是一项检查，
> 有一项不过脚本就非零退出。
> 它们是**独立产物**：`make release` 仍然只产出那六个静态二进制，别的一概没有。
> 设计见 `docs/sandbox-kit-design.md`，构建用 `make kit`。
>
> 写这几组文档时**照代码写**，别照想象写：每个结论都要能在代码里指到行。
> 代码改了，文档跟着改 —— 文档漂移比没有文档更贵。
>
> 要提新东西的设计文档可以写到代码之外 —— 但必须说清哪里是描述、哪里是提案，
> 并把提案标成「尚未实现」。
>
> ★ **两版必须同步**：改英文版就同时改 `_zh` 版，反之亦然。
> 两版的代码标识符、行号引用、命令与路径应当一一对应 ——
> 可以用 `grep -oP '`[a-zA-Z0-9_./:$-]+`' | sort -u` 逐组比对。
> ★ 注意**反引号范围也要一致**：`` `POST /v2/run` `` 与 `` `POST` `/v2/run` `` 讲的
> 是同一件事，但标识符集合不同 —— 比对会报差异。别让「写法不同」掩盖「内容不同」。

## 语言约定（强制）

**所有运行时输出用英文**：日志打印、错误信息、警告信息、面向用户的消息提醒、
**界面文案**。

| 范围 | 规则 | 例子 |
|---|---|---|
| 日志 | 英文 | `obs.Logger().Info("[main] config updated", "version", v)` |
| 错误 | 英文 | `errors.New("query must not be empty")` |
| 警告 | 英文 | `fmt.Fprintln(os.Stderr, "[config] hot reload failed, keeping previous config:", err)` |
| 消息提醒 | 英文 | `c.deferKey("watch.debounce fell back to default: %s", msg)` |
| 接口响应体 | 英文 | `c.JSON(400, map[string]string{"error": "thread not found"})` |
| **界面文案** | **英文** | `"Run in a separate agent"` |
| **写入用户配置的文件内容** | **英文** | `catalog.md` 的表头、`server/config.yaml` 的注释段、`tools/http.yaml` 的注释段 |

### 代码注释

★ **代码里暂不留中文注释。** 本仓库原有一批中文注释，已被整体清掉 ——
注释要**逐点**重新写，**用英文**。

补写注释时遵守这几条：

- **英文**。新写的注释一律英文，与运行时输出同一套语言。
- **写「为什么」，不写「是什么」** —— 尤其是那些被刻意设计成反直觉的地方。
  补注释的价值全在这里：`func New() { … }` 不需要注释，
  「为什么这里不能用 `time.Now()`」才需要。
- **别把框架的语义写进注释当契约**。上一版前端的核心问题就是有一批
  「必须先读框架源码才敢改」的行为，它们全靠注释维持，一改就错。现在
  `lib/sse.ts` / `lib/events.ts` / `lib/store.ts` 三层都不依赖框架，正确性
  由 `bun test` 证明；**新增逻辑请落在这三层里**，让它们保持纯函数。
- **不降低信息密度**。原来用中文写的**为什么**（反直觉的设计取舍、
  踩过的坑、对上游文档的指路）要**逐点**翻过去，不要压缩成一句话。
  `★` 标记保留，它标的是「这里有坑」，丢掉等于把警告降级成闲聊。
- **指路要指设计文档**：`docs/config-design.md`、`docs/api-v2-design.md`
  这类分册才是设计依据，注释别复述它们的内容。

### 范围之外，保持中文

| 范围 | 理由 |
|---|---|
| **测试用例名** | 描述性文本，不是运行时输出 |
| **测试夹具数据** | 断言用的样本值（如 `"答完了"`），改了会让断言与被测行为脱节 |

★ **出厂 agent 提示词（`initcfg/templates/agents/*.md`、`.openheron-example/`）是英文的，
不要改回中文。** 它们既是模型提示词，也是用户读到的文件内容 —— 按上面的规则走。
`inputctx` 构造的 context prompt 本来就是英文（那是给 LLM 读的），不受这条影响。

判断标准：**这段字符串在进程运行时会被打印、返回给调用方，或写进用户读到的文件吗？**
会 → 英文；不会 → 保持中文。

为什么：错误信息常被 grep、被日志采集、被 `llm/common` 的 `Kind` 分类消费；混语言会让排障时的检索与匹配失效，也让 `/v2/config` 的 `deferred` 字段混入两种语言。
界面文案同理 —— 用户看的是界面，不是源码，中英混排会让「这是 bug 还是我配置错了」更难判断。

**日志出口只有一个**：`obs.Logger()`（`github.com/ouranoshong/openheron/agentkit/obs`）。业务包里不要再直接
`log.Printf` / `fmt.Fprintln(os.Stderr)` —— 级别（Info / Warn / Error）与前缀
只有一处定义，而级别决定了一行会不会出现在默认输出里。

例外只有一个：`llm/common` 用标准 logger —— 它是零 openheron 依赖的叶子包，
引 `obs` 就破了那条铁律（`docs/architecture.md` §2.1「两条不能破的依赖铁律」）。

## 其它约定

- **不要在 API 层复制业务装配逻辑** —— 需要读「某个 agent 的生效工具表」时，由 `agent.Builder` 暴露只读查询，不要重算一遍 `Table.Blocked` / `Policy.TierOf`。
- **配置根解析只有三级**：显式 `--config-dir` → `$OPENHERON_CONFIG_DIR` →
  `<projectRoot>/.openheron`（不存在就创建）。**不向上查找祖先目录** ——
  配置根一旦依赖「进程从哪启动」，同一条命令换个目录就读到另一份配置，
  测试实例也会因此读到开发配置。要换配置根就指名，别加新的查找规则。
- **归档优先于呈现** —— SSE sink 断裂后要停止渲染但继续消费事件流，否则 `Outcome.Intermediates` 不完整，历史会缺。
- **缺省档位是 `ask`** —— 没人点名的工具一律要问；例外是那些不用点头就能跑的：
  文件在自己的 `allows:` 里点名的，以及代码知道的那些内置只读工具。★「不被暴露」现在
  只在**两处**说 —— 文件自己的允许清单，和 agent 的 `tools` 清单。**没有全局 deny 了。**
- ★ **agent 自己的几张清单都是白名单，缺省什么都没有。** `tools:` 说这个 agent 能用哪些
  工具，`mcps:` 说它挂载哪些服务器，`skills:` 说它能加载哪些技能 —— 键不写与写空列表是
  同一件事。★ **档位表是另一个问题，而且是共享的**：文件的 `allows:` 说什么不用点头，
  agent 的 `tools` 说什么这个 agent 能用。★ 旧的 `disabled_tools` 键会被**拒绝**，
  而且提示说清两个方向正好相反 —— 只把键名一改，就把黑名单变成了白名单。
- 改配置语义前先读 `docs/config-design.md`；改接口前先读 `docs/api-design.md`；
  新增 `/v2` 接口读 `docs/api-v2-design.md`；动前端读 `docs/frontend-design.md`；
  动测试环境（端口 / 隔离目录）读 `docs/frontend-design.md` §11.2 与
  `docs/config-design.md` §5.2。★ `docs/config-design.md` **就在本仓库里**，
  另外三份是**不在**本仓库的上游文档 —— 拿不到就先问。
  本仓库自带的 `docs/architecture.md` / `docs/user-manual.md` /
  `docs/developer-manual.md`（及其 `_zh` 中文版）讲的是**本代码面**的实际结构，
  直接看就行。`docs/context-usage-design.md` 讲的是上下文占用与压缩：占用仪表盘
  与压缩持久化都已在代码里；动上下文用量相关代码前先看它。

### 提交信息

`Conventional Commits`，一行，英文，不加装饰：

```
feat(ci): install job reads the assets from the artifact
fix(install): the API host is not the web host
docs(readme): drop the API table, it lives in the user manual
```

- 标题写**改了什么、为什么值得改** —— 不列文件清单（diff 会列），也不复述过程。
- 一行。正文可有可无；有的话写**为什么**，不写**做了什么** —— 「做了什么」diff
  已经说了。
- 提交信息是给下一个读它的人留的便条，那个人多半是半年后的你自己。
- 不用表情符号，不用装饰性符号，不用感叹号。

### 前端（`web/`）

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
| `make test-race` | 同一套在竞态检测器下跑（`go test -race ./...`） | — |
| `cd web && bun run test:unit` | 纯函数层（reducer / 分帧 / store / 高亮） | — |
| `make test-e2e` | 真实浏览器 + 确定性假后端 | 无（自带假后端） |
| `make test-install` | 发版安装脚本对着一个本地假发布 | 无 |
| `make smoke` | 真后端联调：SSE 分帧、历史回放、审批接线 | **测试实例在跑** |
| `make lint` | Go vet + gofmt + oxlint + tsc | — |
| `make lint-go` / `make lint-web` | `make lint` 的两半，给只装了一种工具链的 job 用 | — |
| `make check` | `lint` + `test` + `test-race`，提交前跑这个 | — |

★ **CI 跑的就是仓库自己的命令**：`make lint-go` + `make test` + `make test-race`、
`make lint-web` + `bun run test:unit`、`make test-e2e`。冒烟**不在** CI 里 —— 它需要一个答得上话的模型，
而那在 runner 上不可复现。`v*` tag 只有在三项检查全绿之后才发版（见 developer-manual §3.9）。

★ **`v*` tag 也会发布沙箱 Kit。** `kit` job 等的是同一批检查（含密钥扫描），
然后把 workload 与每一份 mixin 推到 Docker Hub。它需要 `DOCKERHUB_USERNAME` 与
`DOCKERHUB_TOKEN` 两个 secret。

★ **密钥扫两遍。** pre-commit 的钩子（`make install-hooks`）扫暂存区；CI 的
`secrets` job 扫全历史。★ 钩子存在之前提交进去的密钥还在历史里 —— 只有 CI 那遍
找得到它。

★ **不要直接推 `main`。** 它受保护：推一个分支、开一个 PR、由仓库主人合并。合并不会发布
任何东西 —— 只有 `v*` tag 才会。

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
cd web && bun run smoke:config             # config_write 的审批往返（有前置条件，见下）
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
  **真正调工具的那个 agent**，其 `write_file` 必须落在 `ask` 档（没被它自己文件的
  `allows:` 点名）**而且**在那个 agent 自己的 `tools:` 清单里 —— 否则模型根本调不出那个
  工具，自然没有审批可触发。
  ★ 别盯着 `main`：出厂配置里 `main` 拿到的是读取类工具与委派工具，
  但它只做编排，会把活**委派**给 `task`，而 `task` 的 `write_file` 在 `ask` 档 —— 审批照样触发。
  2026-10-29 实测的事件流：`TOOL_CALL_START name=task`（子 agent 是以工具形态被调的），
  随后 `TOOL_CALL_START name=write_file subagentRunId=task`，
  中断的 `subagentRunId` 也是 `task`。
  要确认某档是否可达，看**每个** agent 的生效工具表：
  `curl -s :18099/v2/agents | grep -A3 tools` 与
  `curl -s :18099/v2/agents/task`（里面 `effectiveTools` 会给出 tier 与 reason）。
  ★ `.openheron/` 是**本地未跟踪**的配置，改它会影响你自己的真实 agent
  （放开 `write_file` 意味着那个 agent 真能写文件）—— 要改先问自己一句。
- **端口错开不等于数据隔离**。openheron 的会话写在 `.openheron/state/`，测试写
  进去就污染真实历史。测试实例把 `.openheron-test/project` 当项目根并**预置
  自己的 `.openheron/`**，再用 `OPENHERON_CONFIG_DIR` 指名配置根 —— 配置根
  **不向上查找**，所以仓库根那份结构上就读不到。端口与状态必须**一起**隔离。
- **`make test-env-sync` 是必须的，不是可选的**。测试实例里的
  `agents/ llm/ policy/ skills/` 是从开发侧**复制**的，改完 agent 不
  同步，测的还是旧定义。
- **`pkill` 要用 `-x`，不要用 `-f "bin/openheron"`**。`-f` 匹配完整命令行，
  会把执行这条命令的 shell 自己也杀掉（它的命令行里就含这个字符串），
  表现为命令无响应、像是卡住了。正确写法：`pkill -x openheron`。
  ★ **`ps | grep | kill` 是同一种病**：它匹配的也是完整命令行，于是执行这条
    命令的 shell 同样会被杀（命令行里含那个搜索串）。要按名字杀就
    `pgrep -x <name>`（只看 `comm`，不看命令行）。
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

### 本仓库自带（随代码面一起维护）

| 文档 | 内容 |
|---|---|
| `README.md` / `README_zh.md` | **项目说明** —— 定位、快速开始、命令、配置速览、HTTP 速查 |
| `docs/architecture.md` / `docs/architecture_zh.md` | **架构设计说明** —— 分层与依赖方向、配置系统、装配、运行循环、存储、HTTP、前端、知识库 |
| `docs/user-manual.md` / `docs/user-manual_zh.md` | **用户使用说明** —— 安装、配置、界面操作（面向使用者，不含内部设计） |
| `docs/developer-manual.md` / `docs/developer-manual_zh.md` | **开发者说明** —— 分层、依赖铁律、开发配方、测试与排障、已知问题 |
| `docs/context-usage-design.md` / `docs/context-usage-design_zh.md` | **上下文使用预估设计方案** —— 占用口径、数据流、后端与前端改动、压缩结果的持久化与有效历史（**占用仪表盘与压缩持久化已实现**） |
| `docs/session-concurrency-design.md` / `docs/session-concurrency-design_zh.md` | **会话并发设计方案** —— 跨客户端抢占的成因、「一个会话一轮」的规则与实现、以及前端怎么知道会话正忙（**准入闸门与 409 已实现**） |
| `docs/fs-backend-design.md` / `docs/fs-backend-design_zh.md` | **文件系统后端设计方案** —— 为什么把 `eino-ext` 的 local 后端分叉成 `agentkit/fsx`、路径如何收敛在项目根之内、`ls`/`glob` 如何被封顶（**已实现**） |
| `docs/run-lifecycle-design.md` / `docs/run-lifecycle-design_zh.md` | **run 生命周期设计方案** —— 今天一轮为什么绑在一条 SSE 连接上，以及 run 怎么变成一行持久事实、配上租约与 fence，好让连接降级为订阅者（**这是一份提案：§1 讲代码现状，§2 往后的东西还不存在**） |
| `docs/sandbox-kit-design.md` / `docs/sandbox-kit-design_zh.md` | **Docker Sandbox Kit 打包设计方案** —— openheron 进程今天是什么样、一个把整个进程带进 microVM 的 v3 workload Kit、模型密钥如何留在宿主，以及必须实测的 SSE 这条路（**这是一份提案：§1 讲代码现状，§2 往后的东西还不存在**） |
| `docs/mcp-design.md` / `docs/mcp-design_zh.md` | **MCP 接入设计方案** —— 工具装配路径、MCP 客户端给它带来什么、`mcp__<server>__<tool>` 命名空间、工具表里的前缀匹配，以及 MCP 服务器在 Kit 里会跑在哪（**已实施**；例外是 §8 —— 沙箱 Kit 怎么打包一台 MCP 服务器 —— 那部分仍是提案） |
| `docs/config-design.md` / `docs/config-design_zh.md` | **配置系统设计说明** —— 配置根与它的三级解析、各个分区与它们的文件规格、加载与优先级、热更新，以及依赖铁律（**正文讲的是代码现状**；`mcps/` 分区与前缀匹配是提案） |
| `docs/tool-surface-design.md` | **工具面设计**（只有中文）—— 一个 agent 拿到哪些工具（`tools:` / `mcps:` / `skills:`）、MCP 命名空间的三段、配置面收敛成一个核心，以及六个 `config_*` 工具（**这是一份提案：§1 讲代码现状，§2 往后的东西还不存在**） |

★ 无后缀的是**英文主版本**，`_zh` 是中文版；两版内容一一对应，改一处要改两处。

### 上游仓库（不随本仓库分发）

★ 下列文档在**上游仓库**里。引用它们时要看清是上游那份 ——
`docs/architecture.md` **两个仓库都有同名文件**，内容不同：
本仓库那份讲的是**本代码面**的实际结构，上游那份是**总览与分册索引**。

| 文档 | 内容 |
|---|---|
| `docs/implementation-audit.md` | **落地审计报告** —— 哪些没做、哪些是文档漂移（2026-10-23） |
| `docs/hygiene-research.md` | **代码卫生调研** —— 无出口的导出符号、第三方归档、死代码（2026-10-23） |
| `docs/agent-architecture-survey.md` | **外部 Agent 架构调研** —— pi / rakazo / OpenClaw / Hermes / Letta 的 gateway 与后台服务问题（2026-10-24） |
| `docs/positioning.md` | **定位与取舍** —— 专家配专模 × Agent 即工具：机制拆解与边界（2026-10-25） |
| `docs/api-design.md` | 旧的无前缀接口（**已于 2026-10-23 随 M3 下线**，保留为历史记录） |
| `docs/api-v2-design.md` | `/v2` 接口族与 AG-UI 事件契约 |
| `docs/frontend-design.md` | React 前端架构与界面 |
| `docs/agent-design.md` | Agent 池、审批、深度保护 |
| `docs/agent-design-v2.md` | agent 装配 API v2（`deep.Config` 请出 openheron） |
| `docs/multi-agent-design.md` | 多角色、并行委派、按 agent 分账与历史过滤 |
| `docs/config-editor-design.md` | 配置编辑器（`/v2/settings` 六条路由 + 界面） |
| `docs/deep-config-fields.md` | `deep.Config` 字段使用参考 |
| `docs/checkpoint-store.md` | Checkpoint Store：审批恢复的真相源 |
| `docs/kb-design.md` | 知识库 |
| `docs/init-design.md` | `openheron init` —— 二级（项目级）配置的最小可运行初始化 |
| `docs/llm-common.md` | 厂商协议层 |