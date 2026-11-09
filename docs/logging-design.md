# 日志装配改造设计

> ★ **状态：全部落地。** 首次写于 2026-11-09，同日完成 §9 的 1–6。
>
> ★ §2 起描述的设计都已在代码里：`obs` 只剩构造与解析（`New` / `ParseLevel` /
> `LevelFromEnv` / `LevelName` / `Discard`），`agentkit/obs/hertz` 接管了 hertz 的日志，
> `agentkit/obs/httplog` 接管了模型请求日志，`server/config.yaml` 有 `log_level`，
> 每个模块的构造函数都收一个 `*slog.Logger`。`obs.Logger()` / `obs.SetLogger()` /
> `obs.DefaultLogger()` **已删除**，`grep 'obs.Logger()'` 的结果是 0。
>
> ★ **§1 是改造前的快照**，保留下来解释「为什么要改」；读现状请看代码。
>
> ★ 本文只有中文一版，`scripts/check-doc-pairs.py` 的 `CHINESE_ONLY` 里有它；
> 不要给它建 `_zh` 或英文双胞胎。

## 1 代码现状（这一节不是提案）

| 事实 | 落点 |
|---|---|
| 日志等级只从 `$OPENHERON_LOG_LEVEL` 读，构造 Handler 时写死成 `slog.Level`，之后不可变 | `agentkit/obs/logger.go:11`、`agentkit/obs/handler.go` |
| `obs.Logger()` 是全局可变指针 + 互斥锁；`obs.SetLogger()` 可换实例 | `agentkit/obs/logger.go` |
| `installObserver()` 在 `setupConfig()` 之前执行；`hook.New(logger)` 捕获了当时的 logger 实例 | `main.go:150` |
| `server/` 是 `hot: false`、`Editable: false`；`decodeStrict` 开了 `KnownFields(true)` | `agentkit/spec/config.go:70`、`agentkit/spec/config.go` |
| `decl.Server` 没有 `log_level` 字段，模板里也没有这一行 | `agentkit/spec/decl/decl.go`、`agentkit/templates/files/server/config.yaml` |
| hertz 的日志完全没接管：项目里没有一处 `hlog`，引擎横幅与 system logger 走框架默认实现 | `api/server.go` |
| `llm/common` 用标准库 `log` 打请求日志，没有级别标签；理由是它是零 openheron 依赖的叶子 | `llm/common/logging.go:29`、`llm/common/model.go:64` |
| eino 的组件层回调**已经在用**，且已经分级：`hook.levelFor` 把 ChatModel / Embedding / Indexer / Retriever 定为 Info，其余 Debug | `agentkit/obs/hook/hook.go`、`llm/common/model.go` |
| `main` 的四处 `log.Fatalf` 走标准库 `log`，绕开上面这整套 | `main.go` |

★ 两个框架约束（已核对官方 API，不是推测）：hertz v0.10.6 的 `config.Options` **没有 logger 字段**，
唯一入口是 `hlog.SetLogger(FullLogger)`，非并发安全、必须早于 `hserver.New`；eino v0.9.22 的
`callbacks.AppendGlobalHandlers` 是进程级注册表，同样必须在启动时注册。

★ 两条**已经成立的现状**：组件层日志本来就走同一个 logger、本来就分级；`requestLoggingTransport`
在 `http.Client` 内部，处于 eino 组件边界**之下**，callbacks 够不到它 —— 一次模型调用可能对应
多次 HTTP 请求（重试、流式），两者不是一一对应。

## 2 目标语义（已落地）

- 优先级：`$OPENHERON_LOG_LEVEL` > `server/config.yaml` 的 `log_level` > `info`。沿用 `override()`
  与用户手册「环境变量覆盖文件」的既有约定；环境变量名保持不变以兼容现有部署。
- 取值 `debug / info / warn(warning) / error`，大小写与空白不敏感；空值即 `info`。
- 非法值不致命：回退 `info` 并记一条 `deferred` 提示，与 `frontend_mode` 的告警同风格。
  ★ 告警必须在切换等级**之前**发出，否则 `error` 等级会把告警自己吞掉。
- 改配置需重启；`server/` 继续保持只读、不参与热重载。
- 生效范围覆盖所有 slog 输出，包括 hook 与 `With` / `WithGroup` 派生出来的 logger。
  机制是共享一个 `*slog.LevelVar`（Go 1.21+ 并发安全），Handler 持有的是 `slog.Leveler` 而不是定值。

## 3 配置侧（已落地）

★ 三处必须**同批**改：`decl.Server` 加 `LogLevel string`、`defaultServer()` 加默认值 `"info"`、
`agentkit/templates/files/server/config.yaml` 加注释与默认值。少改一处，`KnownFields(true)`
会让启动直接失败，`initcfg` 里模板与代码默认的 `reflect.DeepEqual` 也会挂。

- `loadServer` 解码后用 `obs.ParseLevel` 校验：非法值记 `deferred` 并归一为 `"info"`。
- `api/v2_introspect.go` 的 `serverView` 加 `logLevel`，取 `LevelVar.Level().String()` 这个**生效值**，
  才能反映环境变量覆盖；前端目前不消费 `/v2/config`，加字段是安全的。
- 已有安装的 `server/config.yaml` 不会被 `init` 覆盖，靠默认值兜底；手册要注明「手动加一行」。
- 模板注释块是写进用户配置的内容，按 `AGENTS.md` 的语言规则必须是英文。

## 4 组合根装配（已落地）

`obs` 从「全局单例 + 各模块自己回来取」退化为**纯构造库**：`New(w, *slog.LevelVar) *slog.Logger`、
`ParseLevel`、`Discard`，以及下面两个适配器子包；`obs.Logger()` / `obs.SetLogger()` /
`obs.DefaultLogger()` 在改造结束时全部删除。注入类型直接用标准库 `*slog.Logger`。

```
main(): obs.New(stderr, LevelVar) -> hlog.SetLogger(hertzlog.New(l))
      -> callbacks.AppendGlobalHandlers(hook.New(l))
      -> 子命令 / setupConfig(l) -> LevelVar.Set(env > log_level > info)
      -> setupRuntime(... l) -> api.New(... l) -> hserver.New
```

★ 顺序不能动：`hserver.New` 排最后，hertz 的启动横幅才会按最终等级输出；两次框架交接
必须在任何 hlog / callbacks 使用之前完成。`setupConfig` 自身也会打日志，所以装配必须先于它 ——
这是「先加载配置再重装 observer」行不通的原因：`AppendGlobalHandlers` 只追加，重装会让 hook 重复。

★ 保留两处框架全局注册，不做 per-run 注入（见 §7）。`compose.WithCallbacks` / `InitCallbacks`
的 per-run 方案已从设计中删除，不再考虑。

## 5 logger 依赖注入（已落地）

| 模块 | 注入方式 | 要点 |
|---|---|---|
| `api` | `New(rt, sm, log, opts...)` | `v2Err` / `v2Fail` / `v2FailInternal` 改为 `*Server` 方法；构造点约 6 处（其中 5 处是测试） |
| `agentkit/run` | `run.Config{Logger: log}` | 包级 `logf` 沿 `runRowWriter` 传递，解掉「不是每个调用方都有 Runtime」那句注释 |
| `agentkit/build` | `Builder.WithLogger(log)` | 传播给 `mcpManager` 与 `mcptools` 的告警点 |
| `storage/filestore` | `Options{Logger: log}` | 由 `FileManager` 传播给 `FileSession` |
| `kb` | `NewStore(dir, kb.WithLogger(log))` | 改成变参 Option，保住大量测试调用 |
| `agentkit/emit` | `emit.Options{Logger: log}` | 由 dispatcher 持有 |
| `agentkit/spec` | `NewReloader(svc, cb, spec.WithLogger(log))` | 删 `reload.go` 里包级可变的 `LogReloaded` / `LogReloadFailed` |
| `obs/hook` | `hook.New(log)` | 删掉 `obs.Logger()` 兜底 |
| 子命令 | `kbCmd(...)` / `initCmd(...)` 加 `log` 形参 | `init` 不加载完整配置，沿用引导等级 |

★ **兼容技巧**：所有构造函数零值默认 `obs.Discard()`（写到 `io.Discard`），这样近百处既有构造
调用不用改；只有断言日志的测试需要显式注入 buffer logger。

★ **迁移分四阶段**，避免一次性改爆炸：① 新增 `obs.New` / `LevelVar` / 两个适配器，暂留标记
deprecated 的 `obs.Logger()` 兼容壳，保证全树可编译 → ② 组合根完成两次框架交接 → ③ 从叶子到根
逐包注入，每包配测试 → ④ 删兼容壳，把测试里的 `obs.SetLogger` 全部改成注入。

★ 验收硬指标：`grep -r 'obs.Logger()' --include='*.go'` 命中数为 **0**。
★ 一条支持 DI 的旁证：`Builder.Warm` 与 `Reloader.Watch` 会起后台 goroutine 并打日志，
在它们启动后替换全局 `SetLogger` 就是竞态；组合根先装配正好消掉它，用 `task test-race` 验证。

## 6 把 transport 日志搬出叶子（已落地）

`requestLoggingTransport` 不是协议逻辑，是**观测逻辑**；它住在 L0 叶子包里本身就是架构错位。
比起「给 `llm/common` 注入一个 `*slog.Logger`」，把整包搬出去更干净：

### 6.1 落点与命名

- 新包 **`agentkit/obs/httplog`**：L0 子包，只依赖标准库 `log/slog` 与 `net/http`，
  **连父包 `obs` 都不 import**，与 `agentkit/obs/hertz` 并列成「obs = 日志构造 + 适配器」。
- ★ **不能叫 `transport`**：`transport/` 已经被 L2 的 AG-UI / SSE 事件渲染层占用，撞名会让人
  分不清「事件帧传输」和「HTTP 往返」。
- 导出 `Wrap(c *http.Client, providerID string, log *slog.Logger) *http.Client` 与 `LogRequestsEnv`；
  `nil` logger 退化成丢弃 logger。幂等检查改为 `ok && t.log == log`，否则一个先被 `nil` 包过的
  client 会永远打不出日志。

### 6.2 `llm/common` 的删除清单

- 删 `llm/common/logging.go`（164 行）与 `llm/common/logging_test.go`。
- 删 `llm/common/model.go:64` 的 `hc = WrapHTTPClient(hc, c.ProviderID)`；保留 `Config.HTTPClient`
  并注明「client 由装配层提供已包装的实例」。
- 结果：该包只剩协议代码，**零日志代码、零 openheron 内部依赖**，L0 叶子不变式字面成立。
  不需要给 `common.Config` 加 `Log` 字段，也不需要改脱敏逻辑。

### 6.3 新增的 logger 通道（最容易低估的一块）

包装动作发生在**构造 `http.Client` 的地方**，而这些大多是包级函数、不持有 `Builder`：

| 函数 / 结构 | 改动 | 调用点 |
|---|---|---|
| `Builder` | 加 `log` 字段 + `WithLogger` Option | §5 已有 |
| `toolEnv`（`agentkit/build/tools.go`） | 加 `log` 字段 | `kbToolProvider.Provide` → `buildKBTools` |
| `BuildModel` | 加 `log` 形参 | `agentkit/build/manager.go` |
| `FromConfig` | 加 `log` 形参 | `agentkit/build/provider.go` |
| `NewEmbedder` | 加 `log` 形参 | `agentkit/build/kb.go`、`kbcmd.go` 两处，共 3 处 |
| `snapshot.go` 的 `buildModel` / `resolveModel` | 补包装 client | 非主路径 |

`httplog.Wrap` 只在三处 client 构造点调用：`provider.go` 的**两个**分支（默认 timeout 与
model 覆盖 timeout，别只包一个）、`embed.go`、`ConfigFromEnv` 那条非主路径。

★ 两个不能漏的边界：

1. `common.New` 的 nil-client 兜底会绕过包装，而 `ConfigFromEnv` 不设 `HTTPClient` ——
   非主路径必须补包装，或在文档里明确「这条路径不带请求日志」。
2. ★ 新 wrapper **必须保留 `base == nil → http.DefaultTransport` 回退**：这是
   `docs/sandbox-kit-design.md` 里 `HTTPS_PROXY` 凭据替换链路成立的依据，删掉会断代理。

### 6.4 分级与输出

- 请求摘要 → `Info`（`provider` / `method` / `url` / `bytes` 结构化属性）；
  请求体读取失败 → `Warn`；空体提示与脱敏后的请求体 → `Debug`。
- `OPENHERON_LOG_LLM_REQUESTS` 随包搬走，仍是「是否读取并脱敏请求体」的**内容开关**。
- 请求体仍放在 message 里，由 `obs.Handler` 的 `escapeMessage` 压成单行，脱敏断言保持不变。
- ★ **唯一的行为变化**：请求体现在还要 `log_level: debug`（或 `$OPENHERON_LOG_LEVEL=debug`）
  才可见。原来 `OPENHERON_LOG_LLM_REQUESTS=1 task dev` 的用法要补上等级。若不想动默认行为，
  把后两行映射改回 `Info` 即可 —— 这是落地前需要拍板的唯一取舍。

## 7 全局注册边界与早退（已落地）

- `hlog.SetLogger(hertzlog.New(l))` 与 `callbacks.AppendGlobalHandlers(hook.New(l))` **保留**，
  只在组合根各调用一次，且必须早于 `hserver.New`。这两处是框架契约，不是设计妥协；
  openheron 自己不再持有任何全局可变状态。
- hertz 适配器的 `Fatal*` 映射到 `Error`、**不自行退出**（退出由 `hlog` 的包级包装负责）。
- `main.go` 的四处 `log.Fatalf` 改成注入 logger 的 `Error` + 显式 `os.Exit(1)`，删掉 `"log"` import。
  这要求 `main()` 在任何早退分支之前就建好引导 logger，正好与组合根装配重合。
  `os.Exit` 跳过 defer，与 `log.Fatalf` 行为一致；差别是这几行终于带上 `[Error]` 与正确调用点。
- `agentkit/obs/hertz` 要实现 `hlog.FullLogger` 的 23 个方法：等级映射、格式化参数、
  `obs.Prefix(ctx)`、手工带调用方 PC（否则 handler 打印的是适配器自己的行号）。

## 8 落实之后要回改的文档（第二遍更新）

★ 这一节是给「代码落地后回来改文档」用的清单。**当前这一遍只加标注、不改现状描述**，
因为代码没动，把提案写成现状就是文档漂移。

| 文档 | 现在加了什么标注 | 落地后要改成什么 |
|---|---|---|
| 本文 | 顶部「尚未实现」 | 改成已实现，或并进架构 / 开发者手册后删除本文 |
| `AGENTS.md` / `AGENTS_zh.md` | 文档清单里标为提案；日志出口段加指针 | 删掉「唯一出口 = `obs.Logger()` 与唯一例外 `llm/common`」，改写成「组合根注入 `*slog.Logger`，`obs` 只负责构造」 |
| `README.md` / `README_zh.md` | 中文单份清单里该条标「提案，未实现」 | 改成「已实现」 |
| `docs/architecture.md` / `_zh.md` | §2.1 铁律段加提案指针 | 重写日志出口铁律、删「唯一例外」、改 L0 表格里 `agentkit/obs` 与 `llm/common` 两行，并补「组合根装配」章节 |
| `docs/developer-manual.md` / `_zh.md` | 日志章节加提案指针 | 删「没有级别标签」整段，补「模块只接收 `*slog.Logger`，禁止回取全局」约定 |
| `docs/user-manual.md` / `_zh.md` | §4.5 加提案指针 | 参数表加 `log_level`、环境变量列表补 `$OPENHERON_LOG_LEVEL`、注明「只读 + 需重启」 |
| `docs/config-design.md` | §6.4 与环境变量两处加提案指针 | §6.4 字段表与 YAML 块补 `log_level`；改 `OPENHERON_LOG_LLM_REQUESTS` 的路径引用（文件已搬走）；§8 热重载补一句 |
| `docs/sandbox-kit-design.md` | 本轮不改 | 代码搬包后 4 处行号引用会失效（脱敏、代理回退、往返委派），必须人工核，**脚本不查中文单份文档的行号内容** |
| `sandbox-kit/TUTORIAL.md` / `_zh.md` | 本轮不改 | 可选：`server/config.yaml` 的职责描述补一条 |
| `scripts/check-doc-pairs.py` | `CHINESE_ONLY` 已加入本文 | 若本文被删除，记得从 `CHINESE_ONLY` 里删掉 |

★ 双语文档由 `task lint-docs` 查**标识符集合、代码块、§ 引用数**：反引号跨度必须两版一致
（`` `POST /v2/run` `` 与 `` `POST` `/v2/run` `` 是两种不同的集合）。所有 `file.go:line`
形式的引用都会被脚本扫到并验证行号存在，代码一改就要同步两版。中文单份文档的引用脚本
只查「行号是否越界」，不查内容，得人工核。

## 9 实施顺序与验收

1. ✅ `obs`：加 `New` / `LevelVar` / `ParseLevel` / `Discard`，`handler.go` 的 `level slog.Level` 换成
   `slog.Leveler`，保留 `NewHandler` 旧签名包一层 LevelVar，让现有 `handler_test.go` 不用改。
2. ✅ 新增 `agentkit/obs/hertz` 适配器与 `agentkit/obs/httplog`；暂留 deprecated 的 `obs.Logger()` 壳。
3. ✅ 组合根完成两次框架交接，先让 hertz / eino 日志统一 —— 收益最大、改动最小。
4. ✅ 配置三处同批 + `loadServer` 校验 + `serverView.logLevel`；`main` 接线。`kbcmd` 仍走兼容壳。
5. ✅ 按 §5 从叶子到根逐包注入：`kb` / `filestore` / `emit` / `spec` / `run` / `build` /
   `api` / `obs/hook` / `kbcmd`，构造函数全部收 `*slog.Logger`，零值 `obs.Discard()`。
6. ✅ 四处 `log.Fatalf` 改 `log.Error` + `os.Exit`；删兼容壳；测试里的 `obs.SetLogger`
   全部改成注入（`srv.log`、`b.log`、`emit.Options.Logger`）。
7. ✅ 按 §8 做了第二遍文档更新。

验收：

- `agentkit/obs/hertz/adapter_test.go` 覆盖 23 个方法；`agentkit/obs/httplog` 覆盖等级门限、
  脱敏、`http.DefaultTransport` 回退、幂等、`nil` logger；`agentkit/obs/logger_test.go` 覆盖
  `ParseLevel` 全表、`SetLevel` 对已构造及派生 logger 的实时生效、并发无竞态。
- **黄金测试**：用 `bytes.Buffer` 装配整进程，同时触发一条 hertz 引擎日志、一条 hook 日志、
  一条业务日志，断言三者同格式、同等级门限；把 LevelVar 调到 `error` 后三者同时静默。
  构造 hertz 引擎的测试包需在 `TestMain` 做一次 `hlog.SetLogger`，该全局非并发安全、不能并行设置。
- 两条 grep：`obs.Logger()` 命中数 **0**；`llm/common` 内 `log.` 命中数 **0**。
- `task test`、`task test-race`、`task lint-go`、`task lint-docs`。

## 10 文件输出（2026-11-09 落地）

★ 目标：进程日志除了 stderr 之外还能落到一个按大小轮转的文件里，且 `server/` 的「改了要重启」
语义不变。

### 10.1 为什么是 sink，而不是第二个 logger

logger 在配置读出来之前就建好了（`main.newLogger`），而 `installLogger` 已经把它交给了
hertz（`hlog.SetLogger`）和 eino（`callbacks.AppendGlobalHandlers`）——两个进程级注册表，
运行期不能换。配置文件路径只有读完之后才知道，所以能换的只有 **writer**：
`agentkit/obs/sink.go` 的 `Sink` 实现 `io.Writer`，`Set` 原子改向，`obs.Handler` 与两份
注册表都不动。

`Sink` 同时是失败兜底：目标写失败一次就切回 fallback（stderr），并且只报一次——不重试满盘，
也不让日志静默消失。

### 10.2 接线

| 位置 | 做什么 |
|---|---|
| `main.newLogger` | 建 `obs.NewSink(os.Stderr)`，logger 从第一天就写在 sink 后面 |
| `main.setupConfig` | 在 reloader 回调里用 `sync.Once` 调一次 `applyLogFile`；重载不换路径 |
| `main.applyLogFile` | `Service.LogFile` 解析 + 建目录 + 0600 探针；挂 `lumberjack.Logger`；`console` 决定是否 `io.MultiWriter(os.Stderr, file)` |
| `main.serve` | 退出时 `defer` 关闭 rotator，最后几行日志仍进得去文件 |
| `spec.Service.LogFile` | 相对**配置根**解析；空串原样返回（= 不落盘） |
| `spec.loadServer` | 负数 cap 归一为 0 并记 `deferred`；`file` 去首尾空白 |

★ 探针的理由：`lumberjack` 第一次写入才开文件，且以 0644 创建。不先探一次，权限错误和 0600
都到不了启动期 —— 而启动期是唯一有人能读到那条告警的时候。

### 10.3 取舍

- **默认关**：从终端起服的人本来就在看 stderr；没人要的文件是垃圾。
- **落在配置根**：`logs/` 与 `state/`、`agents/` 同级，都是运行期状态；`static_dir` 是唯一
  指向项目根的路径。
- **`kb` 子命令不写文件**：它把结果打到 stdout，是一次性的；加第二个写者会毁掉「单写者 + 轮转」
  这个假设。
- **`format` 这一轮不做**：JSON 需要第二个 handler，而现在的 `Handler` 是手写的文本封装。
  留作后续迭代 —— 不预留空键，配置里没有 `format`。
