# 把 openheron 打成 Docker Sandbox Kit —— 设计方案

> **状态：一份提案。§2 往后的东西都还不在树上。**
> §1 讲代码现状；§2 往后讲要建成什么样。
> 这份文档记录的决定是：**openheron 的整个进程**跑在 microVM 里，
> 宿主只留一条转发出来的 HTTP/SSE 端口。
>
> 理由在 §2.1 和 §3；短版本是：这是唯一一层**连读路径一起罩住**的隔离，
> 而且对 openheron **一行代码都不用改** —— 边界是往外挪的，不是往里挪的。

---

## 1. 代码现状

### 1.1 openheron 是一个进程、三对外表面

openheron 交付为单个静态二进制（`Makefile:96`、`Makefile:108`）。运行时它是
**一个进程**，对外开三对表面：

- **HTTP/SSE 服务**。`main.go:265` 把它建起来：
  `api.New(rt, fm, hserver.WithHostPorts(st.addr))`。监听地址来自 `main.go:217`：
  `override("LISTEN_ADDR", cfg.Server.ListenAddr)`。
  `POST /v2/run`（`api/v2_files.go:58`）是回合入口；它以 SSE 流应答
  （`api/v2_run.go:146`，`stream := s.newStream(c)`），边跑边 flush
  （`api/v2_run.go:155`、`api/v2_run.go:176`；`transport/sse.go:91`）。
- **文件工具与 shell 工具**。`agentkit/build/snapshot.go:56` 建出同一个后端
  `fsx.New(fsx.Config{Root: s.projectRoot, ExtraRoots: roots})`，
  `agentkit/build/snapshot.go:60-61` 把同一个值交给两个角色：
  `out.backend = local`（文件工具）与 `out.streamingShell = local`（`execute` 工具）。
  shell 那侧跑的是 `exec.CommandContext(ctx, "/bin/sh", "-c", command)`，
  并且 `cmd.Dir = s.root`（`agentkit/fsx/fsx.go:747`、`agentkit/fsx/fsx.go:750`）。
- **模型 API**。`llm/common/model.go:66-72` 把 provider 配置交给 `acl.NewClient`。

Web UI 在**二进制里**：`main.go:35` 内嵌了 `all:web/dist`，`main.go:337` 把它挂在 `/`。
`main.go:333` 是前端没构建时你会看到的警告（`make web`，`Makefile:29-31`）。

★ 由此得出的结论：openheron **不是**一个 CLI 编码 agent。它没有 TUI，没有终端里的
逐命令确认，没有会话选择器。它是一个**浏览器去连的服务**。

### 1.2 今天隔离的单位是"进程"

上面这些全都跑在同一个进程里、同一个用户下、同一个内核上：

- 与启动服务的是同一个用户；
- 同一个内核、同一份文件系统、同一个网络命名空间；
- 与持有模型凭据的是同一个进程。

`agentkit/spec/defaults.go:11` 把 `execute` 放进 `ask` 档：

```go
Ask:  []string{"write_file", "edit_file", "execute", "kb_index"},
```

而工厂的 `main` agent 直接把它关掉（`initcfg/templates/agents/main.md:7`）：

```
disabled_tools: [write_file, edit_file, execute]
```

★ 审批是**策略**边界，不是**技术**边界。批一旦给了，这条命令就拥有 openheron 进程的
全部权限 —— 而 `read_file` 连问都不问。`docs/fs-backend-design.md:166-171` 把道理写得很直白：
读不做收敛，因为 `execute` 本来就能 `cat` 进程读得到的任何文件，收敛 `read_file` 换不来安全。

### 1.3 只有两个可能的接缝

隔离边界只有两个可去的地方，这份文档选其中一个：

- **往里** —— 服务留在宿主，只把 `execute` 工具搬进隔离单元。
  那是 `docs/microvm-execution-design.md`；它只换掉 `StreamingShell` 这一个值。
- **往外** —— 把**整个进程**搬进隔离单元。于是文件工具、shell 工具、模型客户端、
  状态存储全在里面，宿主只留一条转发端口。

★ 这份文档取的是往里之外的那条：**往外的接缝**。因为只有它能关上读路径。
`read_file` 由同一个 `fsx.Local` 提供（`agentkit/build/snapshot.go:60`）；进程在 VM 里，
这次读也在 VM 里。

### 1.4 凭据模型现状

openheron 已经拒绝字面量密钥。写配置的人看到的是 `agentkit/spec/write.go:493`：

```
api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path so the secret stays out of the repository
```

`agentkit/spec/load.go:34-45` 在加载时把引用解析掉：

```go
rawKey := p.APIKey
key, resolved, err := expandValue(rawKey)
...
if !resolved {
	p.APIKey = ""
	c.deferKey("llm/%s.yaml: api_key env %s is not set; calls to this endpoint will fail with 401 Unauthorized",
		id, envVarName(rawKey))
} else {
	p.APIKey = key
}
```

`agentkit/spec/expand.go:13` 是 `@file:` 分支；`agentkit/spec/expand.go:40` 是 `envVarName`。

★ 由此得到两个事实，§2.8 全靠它们：

1. 配置里密钥是一个**引用**，解析出来的值活在跑 provider 的那个进程的**内存**里。
2. **引用解析不出来不是致命错误**。`p.APIKey = ""`，配置照样加载；失败延后表现为 401。

`llm/common/model.go:56-58` 只要求有 base URL ——

```go
if c.BaseURL == "" {
	return nil, ErrNoBaseURL
}
```

—— 然后把 `APIKey` 交给 `acl.NewClient`（`llm/common/model.go:66-72`），
并不要求它非空。`llm/common/logging.go:157` 会在日志里把 `apikey` 与 `authorization` 头抹掉。

### 1.5 会搬进 VM 的那些路径

配置根只有三级（`AGENTS.md`）：`--config-dir` → `$OPENHERON_CONFIG_DIR` →
`<projectRoot>/.openheron`。`main.go:184` 用 `main.go:80-95` 解析出来的两个参数建服务。

进程内部：

- **项目根** —— `main.go:219`，`override("PROJECT_ROOT", cfgSvc.ProjectRoot())`；
  它是 shell 命令里相对路径的基准（`agentkit/fsx/fsx.go:750`），
  也是文件工具收敛的边界（`agentkit/fsx/fsx.go:194`、`agentkit/fsx/fsx.go:219`）；
- **数据目录** —— `main.go:218`，`override("DATA_DIR", cfgDataDir)`；
  会话在 `main.go:224`（`sessionsRoot := filepath.Join(st.dataDir, "sessions")`），
  checkpoint 在它旁边（`main.go:229-232`）；
- **skills 目录** —— 作为第二个根加进来，见 `agentkit/build/snapshot.go:52-54`。

### 1.6 发布模型约束了这次集成

`Makefile:96` 列了六个发布目标：

```
linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
```

`Makefile:108` 用 `CGO_ENABLED=0` 构建它们。发布产物是静态、无依赖的二进制，
在 Linux 上交叉编译产出 —— runner 上没有 C 工具链能给 `darwin/*` 或 `windows/*` 用。

★ 任何需要 cgo 的集成都会破坏发布模型。Kit 不会：Kit 就是一个镜像，
openheron 以同一份静态二进制进到里面。

### 1.7 启动链路，以及每个根从哪里来

openheron 解析三个根，而且是在三个不同时刻解析的。沙箱三个都会碰到，
所以每个的来源值得写清楚：

| 根 | 怎么解析 | 位置 |
|---|---|---|
| 项目根 | `--project-root`，否则取**工作目录** | `agentkit/spec/config.go:157-164` |
| 配置根 | `--config-dir`，否则 `$OPENHERON_CONFIG_DIR`，否则 `<projectRoot>/.openheron` | `agentkit/spec/config.go:170-177` |
| 数据目录 | `$DATA_DIR`，否则 `<configRoot>/state` | `main.go:218`、`agentkit/spec/config.go:224-232` |

★ **工作目录是项目根的默认值，而配置根跟着它走。** 在哪个目录启动的进程，
就是以那个目录为根的进程：`cd` 进工作区，配置就落在 `<工作区>/.openheron`。

★ **`$PROJECT_ROOT` 挪不动配置根。** `main.go:184` 用两个**参数**建服务 ——
`spec.NewService(flags.configDir, flags.projectRoot)` —— 之后 `main.go:219`
才应用 `PROJECT_ROOT` 这个环境变量覆盖；它够得着 agent 的工作目录与文件后端的根
（`agentkit/build/snapshot.go:56`），却够不着配置根。
一个只设了 `$PROJECT_ROOT` 的容器会得到两个不同的根：
agent 在一个目录里干活，配置却从另一个目录读。

★ `openheron init` 是非交互且幂等的 —— 这正是它能当启动步骤用的原因：

- 每个输入都是一个参数（`initcmd.go:16-42`）：`--dir`、`--config-dir`、
  `--model`、`--provider`、`--base-url`、`--env`、`--no-policy`、`--tools`、
  `--force`、`--dry-run`、`--json`；
- 它只写缺的东西，其余报为保留（`initcmd.go:120`、`initcmd.go:147-148`）；
- 它写进 `<dir>/.openheron`，而且**从不读 `$OPENHERON_CONFIG_DIR`**
  （`initcmd.go:57-65`）。

★ 最后这一条是沙箱里的一个坑：`init` 与服务必须对配置根达成一致，
而默认情况下唯一的达成方式就是**共用同一个目录** ——
`init --dir <工作区>` 把配置放在 `<工作区>/.openheron`，
那正是工作目录为该工作区时服务要找的地方。

---

## 2. 要建成什么样

### 2.1 目标

让**整个 openheron 进程**跑在 microVM 里，把项目目录 bind mount 进去，
这样一次跑偏的回合 —— 从网页里捞进来的一条恶意指令、一个依赖安装脚本、
一个被说成"把工作区清理一下"的 agent —— 能损坏的只有项目目录，别的什么都碰不到。

宿主只留一样东西：给 HTTP/SSE 服务转发的一条端口。

非目标：替换审批流程、改 agent 模型、改 API、或者让 openheron 变快。

★ 与 `docs/microvm-execution-design.md` 的差别正是重点。那份文档把服务留在宿主、
只把 `execute` 搬进 VM，加固的是命令路径，读路径照旧敞着（那里的 §2.7）。
这里读路径也在里面，因为提供读的服务进程本身就在里面。

### 2.2 形态：一个 v3 workload Kit

Docker 的 Kit 规范（v3）是打包格式。真正要紧的事实，都取自规范本身：

- Kit 就是**一个 OCI 镜像**。它的 manifest annotation
  `vnd.docker.sandbox.kit.descriptor` 带着描述文件；镜像层带着内容。
  没有 Kit 专属的 media type：不读这个 annotation 的引擎就当普通镜像跑。
- 已发布的 Kit 只有两种。**`workload`** 提供根文件系统与启动契约
  （entrypoint、cmd、env、user、workdir）；**`mixin`** 提供一层叠加。
  一次组合里有且仅有一个 workload。
- 描述文件用 YAML 写；内容配方是普通 Dockerfile 文本。默认的写法是**配对文件**：
  `<stem>.yaml` 与 `<stem>.dockerfile` 并排放，靠文件名主干约定互相找到。
- 解码是**严格**的：任何位置的任何不认识的字段都是错误 —— 一个拼错的键被静静忽略，
  就等于一份策略被静静漏掉。
- 已发布的 Kit 会**把自己的源文件放进镜像**，位置在
  `/usr/share/sandbox/kit/<stem>/kit.yaml` 与
  `/usr/share/sandbox/kit/<stem>/kit.dockerfile`。
- 运行时保证一条平台底线：`bash`、`sh`、`curl`、`git`、一份装好的 CA 证书库，
  以及一个非 root 的默认用户 `agent`，uid `1000`，家目录 `/home/agent`。

openheron 是 **workload**，不是 mixin：它的层就是 agent 干活那台机器的根文件系统。

★ workload 的镜像配置承载启动契约，而规范明确说描述文件不重复它。
这就是 §2.5 能那么短的原因。

### 2.3 三个描述文件

有三个，而这个切分正是重点。真正的文件是
**`sandbox-kit/openheron/openheron.yaml`** —— workload ——
与 **`sandbox-kit/openheron-deepseek/openheron-deepseek.yaml`** /
**`sandbox-kit/openheron-openai/openheron-openai.yaml`** —— 每厂商一份 mixin。
各自配一份自己的配方（若有）；mixin 没有配方，
因为它带的是声明而不是内容。

workload：

```yaml
# syntax=docker/sandbox-kit:3
schemaVersion: "3"
kind: workload
displayName: openheron
description: the openheron agent server, running entirely inside the sandbox
sourceUrl: https://github.com/ouranoshong/openheron
licenses: [Apache-2.0]

args:
  version:
    default: "0.0.6"
    pattern: '^[0-9]+(\.[0-9A-Za-z-]+)*$'
    buildArg: OPENHERON_VERSION

provides: ["openheron@${{ kit.args.version }}"]
version: "${{ kit.args.version }}"

capabilities:
  - type: com.docker.sandbox/sbx@1
  - type: com.docker.sandbox/long-running@1
  - type: com.docker.sandbox/port@1
    config:
      name: http
      container: 8080
      transport: tcp
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - "*"
  - type: com.docker.sandbox/lifecycle@1
    config:
      startup:
        - command: 'openheron init --dir "$WORKSPACE_DIR" || echo "[kit] init did not complete; the existing configuration is kept"'
          env: [WORKSPACE_DIR]
```

mixin —— 纯声明，每个厂商一份。`openheron-deepseek/` 与 `openheron-openai/`
只差厂商名：主机、service、变量名和两处描述字符串。

```yaml
# syntax=docker/sandbox-kit:3
schemaVersion: "3"
kind: mixin
displayName: openheron credential (DeepSeek)
description: the DeepSeek model credential for an openheron workload, injected by the host proxy
licenses: [Apache-2.0]

capabilities:
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - api.deepseek.com
  - type: com.docker.sandbox/credential@1
    optional: true
    description: DeepSeek API access
    config:
      service: deepseek
      phase: runtime
      apiKey:
        name: DEEPSEEK_API_KEY
        proxyManaged: true
        inject:
          - {domain: api.deepseek.com, header: Authorization, format: "Bearer %s"}
```

★ **任意多个厂商 mixin 都可以叠到同一个 workload 上**，而且互不干扰：
allow 列表按阶段取并集，每份 mixin 的凭据有自己的 service 名。
★ 但两个声称**同一个 service** 的 mixin 合不了 —— 运行时会拒绝：

```
error: resolve kits: merge: .../openheron-deepseek (capabilities[1]) and
  /tmp/kit-dup (capabilities[1]) both declare com.docker.sandbox/credential@1;
  one credential has one owner
```

★ **mixin 不带初始化 hook** —— 这是实测结论，不是偏好。
`openheron init` 每个配置根只维护**一份** provider 配置：
一旦根里已有可用的模型，它就复用它并忽略 `--provider`、`--env`、`--base-url`
（`initcfg/initcfg.go:278-286`）。
所以一份想种下第二个 provider 的 hook 会被无视，日志也这么说：

```
> 003-startup-openheron-openai/000-cmd.sh
note: model "deepseek/deepseek-flash" is already declared; it is reused as is
nothing to do: /home/you/my-project is already covered by the existing configuration
```

★ 所以**第二个厂商的配置归用户写** —— 挂载上的 `llm/openai.yaml`，
手写或用配置编辑器都行。
workload 自己的 hook 种下默认 provider；mixin 补上别家的凭据与网络规则。

★ **workload 不声明任何凭据。** 用哪家 provider、哪个端点、哪个变量名，
都是用户的事；而一个只负责交付 openheron 的 workload，没资格替用户挑。
凭据因此移进 **mixin** —— 叠加在 workload 上、带声明而不带内容的覆盖层，每个厂商一份。

★ **网络规则和凭据必须一起走。** 运行时要每个 inject 域名**逐字**出现在同阶段的
allow 列表里，而 workload 的裸 `*` 不满足这条检查：

```
error: resolve kits: kit ".../openheron-deepseek": effective v3 descriptor:
  capabilities[0].config.apiKey.inject[0].domain: capabilities[0] (deepseek):
  inject domain "api.deepseek.com" is not in the network policy's runtime allow list
```

所以 mixin 把主机名写实，而两张 allow 列表按阶段取并集：
workload 保留 `*`（"这个 Kit 不限制"），mixin 补上它需要的主机。
这正是 mixin 该干的事。

★ **mixin 是可选的（要用几家就组合几家），而这正是切分的意义。**

| | 容器里是什么 | 真密钥进不进 VM |
|---|---|---|
| workload **+** credential mixin | `proxy-managed` | 不进 |
| 只有 workload，密钥自己注入 | `sk-…` | **进** |

★ 而且哨兵值**有没有绑定都在**：容器总是知道自己该用哪个变量，
只有**真值**才需要绑定。没有绑定的厂商 mixin 差一个 401，不是一个坏掉的沙箱。

★ 几处不是装饰的地方：

- **`com.docker.sandbox/sbx@1`** 声明这个 workload 是准备好让宿主按 agent 的方式启动的，
  而承载启动契约的正是它 —— 基础镜像必须提供的 shell 与用户账户。
- **`com.docker.sandbox/long-running@1`** 让 workload 不依赖挂着的会话而持续运行。
  openheron 是服务，不是会话：它得起来、得服务、得一直活着。
- **`com.docker.sandbox/network-policy@1` 带 `allow: ["*"]`** 说的是这个 workload
  自己**不限制** agent 能够到什么地方（§2.7）。
  ★ "什么"就是"不限制"的可移植写法：某个阶段整块省略等于那个阶段什么都没有，
  所以"什么都不写"恰恰是"不写放开"的反面。
- **`com.docker.sandbox/lifecycle@1`** 是初始化待的地方（§2.5）：
  一个 `startup` hook，所以它每次启动都跑、以 uid `1000` 跑，
  并且可以依赖 `openheron init` 的幂等。
  hook 的环境默认拒绝，所以它读的那个名字要声明出来。
- **版本参数是唯一的参数。** provider、模型 API 的主机、以及 api key 环境变量的名字
  都是 openheron 自己的规则（§2.7）—— 而它们现在住在 mixin 里，不在 workload 里。
- **没有 `agent-context@2`。** openheron 自己的说明在配置根里
  （`agents/main.md`、`agents/task.md`），运行时没什么可交给它的 ——
  而且声明了它的 Kit 还得扛得住一个不实现它的运行时。

### 2.4 Dockerfile

配方的活儿很小：提供平台底线，把 openheron 放进去，说明怎么启动。
真正的文件是 **`sandbox-kit/openheron/openheron.dockerfile`**：

```dockerfile
# syntax=docker/dockerfile:1
#
# The content recipe for the openheron workload Kit.
#
# ★ The base must provide the platform floor: the `agent` user at uid 1000,
#   /home/agent, bash, git and a CA store. A workload that ignores this
#   "builds fine and fails at agent launch".

FROM docker/sandbox-templates:shell

ARG OPENHERON_VERSION
ARG TARGETARCH

USER root
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# ★ The release asset is the same static, CGO_ENABLED=0 binary the six release
#   targets ship (Makefile:96 and Makefile:108). The Kit adds no second build.
RUN curl -fsSL "https://github.com/ouranoshong/openheron/releases/download/v${OPENHERON_VERSION}/openheron_v${OPENHERON_VERSION}_linux_${TARGETARCH}.tar.gz" \
    -o /tmp/openheron.tar.gz \
    && mkdir -p /tmp/openheron \
    && tar -xzf /tmp/openheron.tar.gz -C /tmp/openheron --strip-components=1 \
    && mv /tmp/openheron/openheron /usr/local/bin/openheron \
    && rm -rf /tmp/openheron /tmp/openheron.tar.gz

# ★ The entrypoint is copied as root: the agent user could not chmod a
#   root-owned file.
COPY entrypoint.sh /usr/local/bin/openheron-entrypoint
RUN chmod 0755 /usr/local/bin/openheron-entrypoint

USER agent
WORKDIR /home/agent/workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/openheron-entrypoint"]
```

它拷进去的入口脚本是 **`sandbox-kit/openheron/entrypoint.sh`**：

```sh
#!/bin/sh
# ★ The workspace path is dynamic; WORKSPACE_DIR is how the runtime names it.
#   The image's WORKDIR is a fixed path, and starting there would put the
#   configuration inside the VM instead of on the user's mount.
cd "${WORKSPACE_DIR:-$PWD}" || exit 1

# ★ init is idempotent: it writes what is missing and keeps what is there.
#   The startup hook already did this; repeating it costs one no-op and makes
#   the result independent of where the hooks sit relative to the entrypoint.
#
# ★ No --provider and no --env: openheron init decides both from its own
#   rules, and the configuration on the mount is the user's to edit.
openheron init --dir "$PWD" || \
    echo "[entrypoint] init did not complete; the existing configuration is kept"

exec openheron
```

几点：

- 基础镜像**必须**满足平台底线：uid `1000` 的 `agent` 用户、`/home/agent`、
  `bash`、`git`、CA 证书库。规范说，workload 不满足这条就是"构建顺利，启动时失败"。
- 二进制就是 `Makefile:96` 那份发布产物 —— 与六个目标发的是同一份静态、
  `CGO_ENABLED=0` 的构建（`Makefile:108`）。Kit 不引入第二套构建。
- `TARGETARCH` 选 `amd64` 或 `arm64`，对应两个 Linux 目标。
- CA 证书库是要紧的：模型客户端走 TLS，`llm/common/error.go:133` 告诉用户去查
  "network and TLS are healthy"。
- **先 `cd`，其余都跟着来**：工作目录就是项目根（§1.7），
  配置根是 `<cwd>/.openheron`。
- **入口脚本故意再初始化一遍**。`openheron init` 是幂等的，
  所以在 startup hook 与入口脚本里各跑一次，代价是一次空操作，
  却把"hook 与入口脚本谁先谁后"这个依赖去掉了（§2.5）。
- **`EXPOSE 8080`** 声明端口；把它发布出去是运行时的事
  （`sbx ports --publish 8080:8080`），不是 Kit 申请的一项 capability。

### 2.5 启动契约

启动契约由镜像配置承载；描述文件不重复它。规范里的两种启动模式：

| 模式 | Argv |
|---|---|
| 默认（headless / task） | 镜像 `Entrypoint` + 镜像 `Cmd` |
| 交互式（TTY） | 镜像 `Entrypoint` + lifecycle `interactive` |

openheron 是 **headless**：没有交互模式要声明，`interactive` 字段留空即可。

★ **工作区是挂在 VM 里的同一个绝对路径上 —— 但 workload 并不从那里启动。**
镜像配置拥有 `workdir`，而一个 workload Kit 完全拥有这些字段。
"agent 从工作区启动"是**内置** agent 的行为，Kit workload 跑在它镜像说的地方。

★ 所以 Kit 有一件 Dockerfile 自己做不了的活：让进程**从工作区启动**。
下面其余内容都是这一件事的推论。

容器拿到的环境变量 —— 以及 Kit **没有**设的那些：

★ **Kit 一个 openheron 环境变量都没设。** 入口脚本 `cd` 进工作区，
openheron 自己的默认值从那里落到了正确的位置：

- `PROJECT_ROOT` —— 默认是工作目录（`agentkit/spec/config.go:157-164`）。
  入口脚本的 `cd` 把它放在挂载上。
- 配置根 —— 默认 `<projectRoot>/.openheron`（`agentkit/spec/config.go:170-177`），
  所以就是工作区自己的 `.openheron`，在挂载上。
- `DATA_DIR` —— 默认是配置根下的 `state`（`agentkit/spec/config.go:224-232`），
  所以会话也住在挂载上（§2.9）。
- `LISTEN_ADDR` —— 默认取服务配置的 `ListenAddr`（`main.go:217`）：`:8080`。

★ 来自**运行时**（而不是来自 Kit）的变量：`HTTPS_PROXY` 与 `HTTP_PROXY`
（openheron 认它们，§2.7；凭据替换正是靠它们，§2.8），以及 `WORKSPACE_DIR` ——
工作区挂载的路径。`WORKSPACE_DIR` 也是启动 hook 声明的那个名字（§2.5）。

★ **`main.go:459-464`（`override`）是另一种摆法能成立的原因。**
`LISTEN_ADDR`、`DATA_DIR` 与 `PROJECT_ROOT` 都**先**从环境变量读、
**后**从配置文件读，所以想换成别的摆法（比如把会话放到 volume 上，§2.9），
用户只要设 `DATA_DIR`，别的都不用改。

★ **`$PROJECT_ROOT` 刻意不在 Kit 设的那几项里。** 它挪不动配置根（§1.7），
设了反而会让容器有两个根。

#### 第一次运行：初始化

一个空工作区没有配置，而服务没有配置就不肯启动（`main.go:135-143`）。
所以 Kit 必须在服务起来之前把配置种下去 —— 而且要种在**工作区**里、
以 uid `1000` 种，因为工作区属于用户。

`openheron init` 是非交互且幂等的（§1.7），正好合适。
它只需要知道一件事：工作区在哪儿。
写哪个 provider、哪个环境变量名，都是 openheron 自己的规则（§2.7），所以 hook 两个都不问。

★ **`lifecycle@1` 就是工作区路径的来源。** 这个 capability 的 hook
环境是**默认拒绝**的 —— hook 只看得到自己声明的名字 ——
而规范自己的示例就是按 `WORKSPACE_DIR` 这个名字把它传给 hook 的。
这里要紧的规则是：

- `install` hook 每个沙箱**只跑一次**，默认以 **root** 运行；
- `startup` hook **每次启动都跑**，默认以 **agent 用户（uid `1000`）** 运行；
- startup hook 的作者必须让它幂等 —— 每次启动都跑，说的就是每次启动 ——
  而 `openheron init` 正是如此。

★ 所以初始化该待在 **`startup` hook** 里：默认 uid `1000`、每次启动都跑、
天生幂等。`install` hook 在两个维度上都是错的阶段 ——
它只跑一次（重建的沙箱就没东西可种了），而且它以 root 运行
（文件会以 root 的身份落在用户的挂载上，agent 反而写不进去）。

```yaml
  - type: com.docker.sandbox/lifecycle@1
    config:
      startup:
        - command: 'openheron init --dir "$WORKSPACE_DIR" || echo "[kit] init did not complete; the existing configuration is kept"'
          env: [WORKSPACE_DIR]
          description: Seed the project configuration in the workspace
```

★ 用 `|| echo ...` 而不是 `&&`：当已有的 provider 文件声明了另一个模型时，
`init` 会失败（`initcfg/initcfg.go:94-99`），
而那是一份该保留的配置，不是拒绝服务的理由。

★ **待决的问题是：入口脚本怎么知道工作区路径。** hook 看得到 `WORKSPACE_DIR`；
而 workload 自己的环境里带不带它，规范没有说。于是有两种形态：

- **workload 的环境带 `WORKSPACE_DIR`** —— 那入口脚本一个人就能干完：
  种下配置、`cd` 进工作区、导出 `OPENHERON_CONFIG_DIR`。不需要 hook，
  也没有顺序问题。
- **不带** —— 那就得由 hook 把路径交给入口脚本（hook 写一个文件），
  于是 hook 与入口脚本的先后顺序必须成立。
  规范要求 **install** hook 必须在入口脚本首次运行前跑完，
  也把 **startup** hook 描述为每次启动的一部分，
  但没有说明 startup hook 相对入口脚本的位置。

★ 两种形态下入口脚本都还是两行，区别只在其中一行。
§4 的第一个问题是按哪种形态来建；§5 的第二个问题是怎么观测出答案。
那是 §4 的第一个问题。

★ **实测：第一种形态。** workload 的环境带 `WORKSPACE_DIR`（§6.3），
所以入口脚本自己就能干完，hook 是冗余而不是依赖。

#### 服务答在哪个端口上

★ **要对外服务的 workload 必须声明端口。** `com.docker.sandbox/port@1`
就是那句"这儿有个服务"；没有它，运行时没有可转发的服务，
沙箱是起来的，只是没人应答。

```
error: resolve kits: kit "./sandbox-kit/openheron": effective v3 descriptor:
  capabilities[2].config.container: 8080 is required
```

★ 这份声明只有四行，而宿主侧**不**在其中：宿主端口由运行时自己分配，
在启动时给一个临时的回环端口。Kit 不能钉死宿主端口 ——
两个 Kit 要同一个就会撞 —— 所以端口是用户从 `sbx ports` 里读出来的，
而不是他选的。

```
✓ http — published localhost:32772 -> 8080/tcp
```

★ **family 留空，而留空就是实话。** 默认值是 `ipv4`；openheron 监听的是
`[::]:8080`，那是双栈套接字，IPv4 连接也进得去，
所以客户端够到发布的 IPv4 绑定就等于够到了服务。声明 `dual` 说的是同一件事，
但会被拒：

```
error: resolve kits: kit "./sandbox-kit/openheron": port 8080: family "dual"
  is not supported yet; omit it
```

★ **被运行时拒掉的描述文件意味着沙箱根本起不来**，所以 Kit 声明的是运行时
今天实现得了的那一份，而两者的差异记在该能力的说明页里。

#### 到底是什么把服务拉起来的

★ **入口脚本就是 agent 的启动命令，而拉起它的是一次 agent 会话。**
规范说得很明确：`sbx@1` 要求宿主不要让镜像的入口脚本成为 PID 1，
而要把它读成"稍后要跑的命令"。于是：

- `sbx run … --detach` 建起沙箱、跑完启动 hook，却**不开 agent 会话** ——
  所以没有东西把服务拉起来。沙箱是配好的，也是哑的。
- 不带 `--detach` 的 `sbx run …` 会开 agent 会话，服务随之启动。

★ **而且它会一直活着。** 靠的是 `long-running@1`：运行时不得仅因为没有会话
挂着就停掉持有它的沙箱。于是客户端可以分离 ——
交互会话本身就有一个分离手势 —— 而服务继续在跑、继续应答，
直到沙箱被停掉或删掉。

★ 这就是把 openheron 当沙箱 workload 跑的整个形状：**会话把它拉起来，
能力把它留住。** 想要服务一直在线的用户，开一次会话然后从它分离。

### 2.6 VM 里的路径语义

工作区挂在与宿主**同一个绝对路径**上，而 Kit 让进程从它**里面**启动（§2.5）。于是：

- `cmd.Dir = s.root`（`agentkit/fsx/fsx.go:750`）含义不变：guest 的工作目录
  还是同一个目录，拼写方式也一样；
- shell 命令里的相对路径仍然指项目根；
- 配置根是 `<工作区>/.openheron` —— 既是用户在宿主上编辑的那个地方，
  也是 `openheron init` 写入的那个地方（§1.7）；
- 报错里引用的路径，仍然是用户看到的那几个路径。

★ guest 路径一旦和宿主路径不同，模型被告知过的每一个路径
（`docs/user-manual.md`、agent 提示词）都会在**最不该错的地方**错掉。
★ 而且配置就不再是用户管的了：落在挂载之外的配置根，是会随沙箱一起消失的配置根。

★ **这一步没有退路。** 路径是动态的 —— 它就是用户那个目录 ——
所以它没法烤进镜像里，而镜像的 `workdir` 是一个固定路径，
用它就会把配置关在 VM 内部。运行时必须告诉进程工作区在哪儿，
而它记录下来的机制就是 `WORKSPACE_DIR`（§2.5）。

### 2.7 网络面

★ **这个 Kit 守的规矩：边界是隔离，不是允许列表。**
microVM 已经把 agent 与宿主隔开了。沙箱里再筑一道墙，
买到的多半是摩擦而不是安全 —— 一个装不了依赖、连不上自己 MCP server 的 agent，
干不了它被交付的那份活。所以 Kit 的 `network-policy@1` 给运行阶段放 `*`：
**这个 Kit 自己不对出网设任何限制。**

★ **"什么都不写"不等于"放开"。** 运行时是默认拒绝的，
而"某个阶段整块省略 = 那个阶段什么都没有" ——
所以省略掉这项 capability，agent 反而**一点出网都没有**。
`*` 才是把"我不管"这件事表达出来的写法。

真正决定 agent 能到哪里的，是：

- **宿主的策略** —— `sbx policy`，以及登录时选的档（Open / Balanced / Locked Down）。
  那是用户的旋钮，且它作用在 Kit 的请求之上。
- **组织的规则**（若有）—— 那里 kit 与本地允许规则都不生效，
  只有组织规则放行，而 deny 始终生效。
- **沙箱自身的网络姿态** —— 私有网段、回环与链路本地地址都不可达，
  非 HTTP 协议在网络层就被拒。那是运行时的，不是 Kit 的。

★ **Kit 唯一指名的是密钥该落在哪儿。** 凭据的 inject 域名是模型 API 主机 ——
那是宿主的代理往请求里换真密钥的地方，不是对 agent 触及范围的限制。
用户把配置改到别的主机，那个主机照样够得到 —— 只是模型密钥不会被带过去，
于是那次调用会以 401 收场，除非把 inject 域名一并改成对应的。

openheron 自己的出站需求不因这个而改变：

- **模型 API 主机** —— 来自 provider 配置的 `base_url`。
  工厂模板把它也做成了模板变量：`initcfg/templates/llm/deepseek.yaml`
  渲染出 `base_url: https://api.deepseek.com`，
  `initcfg/templates/llm/openai.yaml` 渲染出 `https://api.openai.com/v1`，
  而 `openheron init` 会问用户填哪个（`initcfg/initcfg.go:100-106`）。
  `inject` 规则按 **domain** 匹配，所以 `base_url` 里带不带路径不影响（§2.8）；
- **工具**够到的一切 —— HTTP 工具（`agentkit/spec/defaults.go:16-27` 注册了
  `http_get` / `http_post` / …）、MCP server，以及 agent 装包时用的仓库。
  ★ 有了 `allow: ["*"]`，它们与别的东西走同一份放行 ——
  而它们正是"允许列表不该写短"的原因。

★ **诊断要看宿主的策略日志**，不是看 Kit。它显示什么被拒了；
在比 `*` 更窄的宿主策略下，缺哪些主机就在那里露出来。

★ **代理就是全部的模型。** 每个数据包都从宿主侧的代理出去，
代理既执行策略也注入凭据 —— 所以"网络策略"和"代理"是一件事，不是两件事。
openheron 的客户端走的正是它：`llm/common/model.go:60-63` 建的是一个
没有自带 transport 的普通 `http.Client`，`llm/common/logging.go:36-38`
回退到 `http.DefaultTransport`，`llm/common/logging.go:77` 把往返委派给它 ——
而 `http.DefaultTransport` 会从环境变量读 `HTTPS_PROXY` 与 `HTTP_PROXY`。

★ 这正是凭据替换能成立的原因（§2.8），也是要盯着的失败形态：
一个忽略代理的客户端会把**哨兵值**直接送到 API，
而 API 回一个看起来像"密钥无效"的 401。

### 2.8 凭据这条路 —— 两种跑法

★ Kit 用的机制是 `credential@1` 带 `apiKey.proxyManaged: true`，
规范对它做了什么说得很精确：

- `apiKey.name` 是**容器内的环境变量名**；
- 有了 `proxyManaged: true`，真值**留在宿主**，容器看到的是**哨兵值** ——
  `proxy-managed`，一个非空的占位符，不是密钥；
- 边界（宿主侧的代理）在出站请求命中 `inject` 规则时把真凭据换上去；
- 宿主的密钥库是唯一的来源 —— 规范的措辞是"a Kit declares the need, the
  user's bindings answer it"，并且"host environment variables never
  auto-inject; a Kit cannot name where a secret lives, only what it needs"。

这正是 openheron 现在的形状：

1. `agentkit/spec/load.go:34-45` 通过 `agentkit/spec/expand.go:24-33` 解析
   `api_key` —— 那段代码把 `${...}` 里的名字在进程环境里查出来；
2. `llm/common/model.go:66-72` 把它作为 bearer token 发出去。

★ **两种跑法。** 它们只差一件事 —— 容器最后拿着什么 ——
而那件事决定了 agent 能不能读到密钥。两种都实测过（§6）。
凭据本身住在 mixin 里（§2.3），所以第一种跑法是"组合 mixin"，
第二种是"不组合"。

| | 容器里是什么 | 真密钥进不进 VM |
|---|---|---|
| **占位符** | `proxy-managed` | 不进 |
| **真密钥** | `sk-…` | **进** |

#### 2.8.1 运行时注入的占位符

运行时把哨兵值放进容器；宿主拿着真值，两边得对上占位符。
一条命令就够了：

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ `--placeholder proxy-managed` 就是全部机关：它让宿主那边的占位符
**与运行时注入的是同一个字符串**，两边自动对上，用户完全不必再传环境变量（§6.4）。
容器里最后是 `DEEPSEEK_API_KEY=proxy-managed`，
宿主侧的代理在出站路上把真值换上去。
`--value-stdin` 让密钥既不进进程参数，也不进 shell history。

★ custom secret 的替代是 **service 绑定**，而它要两步而不是一步：
存下来的密钥只是**值**，**授权**在另一个文件里。
mixin 里的 `service` 名就是那份绑定要用的键。

```sh
sbx secret set deepseek      # the value
```

```yaml
# ~/.config/sbx/credentials.yaml — the authorization
bindings:
  deepseek:
    apiKey:
      domains:
        - api.deepseek.com
```

没有那个绑定，沙箱照样起来，而每次模型调用都是 401 ——
§2.3 那个 `optional: true` 的降级，比预期来得早。

#### 2.8.2 真密钥本身，当作普通变量注入

另一种跑法是跳过凭据机制：让运行时带着密钥走。

```sh
sbx run --detach --name openheron --env DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" \
    ./openheron.yaml .
```

运行时变量给什么就是什么，所以这会把**真密钥放进 VM** ——
agent 可以从环境里把它读出来，它跑的任何东西都能把它发到任何地方。
于是模型调用照样成功 —— 容器里已经是正确的 bearer token 了，
代理发现它不是占位符，就原样转发。

★ Kit 的 `credential@1` 声明不会挡路：注入的值盖过了这个 capability
本来要装进去的哨兵，所以同一个 Kit 两种跑法都能用。

#### 2.8.3 两者共有的部分

- 凭据的用途始终在权限面里（§2.14）—— 组合 mixin 时在 mixin 里，
  不组合时在 workload 的"没有它"里。
- **inject 域名不是对 agent 的限制**：它说的是**密钥**该落在哪儿。
  用户把配置改到别的主机，那个主机照样够得到 —— 只是模型密钥不会被带过去，
  于是那次调用会以 401 收场，除非把 inject 域名一并改成对应的（§2.3）。
- 一个沙箱一条凭据：一个 (service, phase) 只有一个主人，
  所以两个厂商不能共用一个 service 名。要第二家，就得换 service 名，
  也就得第二个 Kit 或一个 mixin。

### 2.9 沙箱拆除之后的状态

会话与 checkpoint 是数据目录下的文件（`main.go:224`、`main.go:229-232`），
而数据目录的默认值是 `<configRoot>/state`（`agentkit/spec/config.go:224-232`）。
既然配置根是 `<工作区>/.openheron`（§1.7），默认的数据目录就是
**`<工作区>/.openheron/state`** —— 在挂载上。

★ 这一点值得直说：**默认就已经是持久的。** 挂载属于用户、比沙箱活得久，
所以对话能挺过沙箱的停止、重建，甚至移除。
规范里的 `volume@1` capability 在这件事上并不需要。

状态有两个落处，行为不一样：

| 数据目录在哪 | 会话能挺过 |
|---|---|
| 在挂载上（默认） | 停止、重建，**以及**移除 |
| 在 `volume@1` 实例上 | 停止与重建，但挺不过移除 |

★ 这笔账不是"持久还是不持久"，而是**状态放在哪**。
在挂载上，状态待在用户自己的目录里、和他的代码放在一起，
他可以读它、备份它，或者连同其余文件一起删掉。
在 volume 上它不碍事，但也在用户够不着的地方。

★ `DATA_DIR`（`main.go:218`）就是那个开关：绝对路径优先于默认值
（`agentkit/spec/config.go:229-232`），
所以 Kit 可以把数据目录指到 volume 上，而不必碰配置。

★ 默认值有一个后果：状态落在用户自己的目录树里，在 `<工作区>/.openheron`。
那个目录没有理由进版本库。

### 2.10 SSE 这条路 —— 风险所在

这是整套设计最可能塌掉的地方，值得直说而不是埋起来。

回合流是一个 SSE 响应。`api/v2_run.go:146` 建流，`api/v2_run.go:155` 与
`api/v2_run.go:176` flush，`transport/sse.go:104` 用 keepalive 维持连接。
浏览器会在整个回合期间持有这条连接。

浏览器走的那条路是：浏览器 → **宿主端口** → 沙箱 → openheron 的 hertz 服务。
如果宿主与沙箱之间的转发层**缓冲**了响应体，模型就不再流式输出：
用户什么都看不到，直到回合结束，然后一次性全部涌出。

★ 这件事不由本仓库决定。它是转发层的属性，必须**实测**（§5），不能假设。
若它确实缓冲，可选项是换一种会 flush 的转发配置，
或者换一条浏览器与沙箱之间的传输方式。

这与浏览器在 `make smoke` 里已经面对的是同一个风险 —— 穿过真服务的真 SSE 流 ——
而它在 E2E 的假后端里**不存在**，所以 E2E 套件回答不了这个问题（§5）。

### 2.11 它不改变什么

边界往外挪，这正是它便宜的原因。**Go 代码一行不改。** 下面这些一个都不动：

- agent 模型、策略档位（`agentkit/spec/defaults.go:11`）、审批流程、checkpoint store；
- 文件工具及其收敛（`agentkit/fsx/fsx.go:194`）；
- API 面（`api/v2_files.go:34-72`）；
- 前端，它是内嵌的（`main.go:35`）；
- 发布模型（`Makefile:96`、`Makefile:108`）。

★ 变的是**进程跑在哪里**，以及因此宿主能看见什么。
这是这次变更最便宜的一种形态，也正是这份文档值得在写任何代码之前写的原因。

### 2.12 平台矩阵

| 宿主 | Docker Sandboxes | 后果 |
|---|---|---|
| `linux/amd64`、`linux/arm64` | 需要 KVM | 在服务器或有 `/dev/kvm` 的 Linux 工作站上可用 |
| `darwin/arm64` | Apple Silicon + Hypervisor.framework | 可用 |
| `darwin/amd64` | **不支持** | Intel Mac 没有 microVM 这条路 |
| `windows/amd64`、`windows/arm64` | Windows Hypervisor Platform | 可用，但有注意事项 |

★ Intel Mac 正是 `docs/microvm-execution-design.md` §2.8 为 microsandbox 那条路记下的同一个洞。
两个提案、同一个洞：发布矩阵有六个目标，microVM 隔离够得着五个。

差别在于谁来填这个洞。microsandbox 那条路把服务留在宿主，
所以在 Intel Mac 上**宿主那条路仍然能用**，缺的只是 `execute` 的隔离。
这里整个进程都在 VM 里，于是没有退路：在 Intel Mac 上，
openheron-as-a-Kit 根本跑不起来。

★ 这是个真实的取舍，要拿它去和 §2.11 的"不用改代码"对照。
Intel Mac 上的用户继续用普通二进制；进 Kit 的用户拿到完整的边界。

### 2.13 分发

- Kit 是一个 **registry 产物**。`sbx run <image>` 消费它；
  已发布的描述文件是签名覆盖的对象，也是权限闸门评判的对象。
- Kit 要求宿主上装了 `sbx` CLI。这对 openheron 用户是一个新的安装步骤 ——
  而且与 microsandbox 不同，它还带来一次 **Docker 登录**，
  以及一份对个人与商用免费、但对组织级治理单独计价的许可。
- 权限面是一份**可评审的 diff**：规范的闸门会拦住任何扩权 ——
  新增允许域名、新增凭据、删掉一条 deny —— 并转人工确认。
  对一个能跑任意命令的 agent 来说，那份 diff 正是安全评审该看的东西。

### 2.14 边界自身的风险

★ 上面说的都是边界**买到**什么。这一节讲它**付出**什么 ——
只算前者的设计，不算账。

**这条边界有过已公开的逃逸。** Docker 自己的安全公告页记着 Docker Sandboxes 的两个缺陷，
两者都在 2026-09-07 的 `0.42.0` 版里修好，并于 2026-09-15 公开：

- `CVE-2026-77179`（CVSS 9.4，严重）—— 在 macOS 上，virtio-fs 宿主服务
  在按存下的路径重开一个已 unlink 的文件时会跟随符号链接。
  恶意 guest 可以把父目录换成符号链接，逃出共享工作区，
  以 VMM 用户身份读写宿主的任意文件，可能进一步达成宿主代码执行。
  受影响版本 `0.28.0` 起、到 `0.42.0` 之前为止。
- `CVE-2026-79994`（高危）—— Unix socket 中继里的一个
  time-of-check to time-of-use 竞态：guest 可以让宿主去连
  共享工作区之外的 `AF_UNIX` socket。受影响版本 `0.37.0` 起、到 `0.42.0` 之前为止。

★ 官方给这两条的缓解办法是**clone 模式并且不要加读写主机挂载** ——
而这恰恰是 openheron 用不了的：agent 必须往工作区里写，
也必须往用户要编辑的那份配置里写。
所以对这个负载来说，缓解办法不可用，答案只能是版本下限：
**`sbx` 0.42.0 或更新**，在发布 Kit 之前就查。

★ 这条边界是强边界，不是绝对边界，而且它跑得很快。
同一份公告页还记着一次证书机构被攻破，迫使内置 kit 镜像重建 ——
所以 Kit 的基础镜像是需要刷新的供应链产物，不是一个常量。

★ **agent 造出来的文件在宿主上归 uid `1000` 所有。**
workload 以 `agent` 用户（uid `1000`）运行，而挂载并不带 openheron 能依赖的 uid 映射：
agent 在工作区里写下的文件，在宿主视角下属于 uid `1000`，
而那在多数 Linux 宿主上是另一个毫不相干的账号。用户读得了；
要改之前可能得先 `chown`。这一条值得实测而不是假设（§5）。

---

## 3. 与 microsandbox 提案的关系

两个提案，一个目标，形状不同：

| | `docs/microvm-execution-design.md` | 本文档 |
|---|---|---|
| 隔离的是什么 | `execute` 工具 | 整个进程 |
| 服务跑在哪 | 宿主 | microVM 里 |
| 代码改动 | 换掉 `StreamingShell` | 没有 |
| 读路径 | 仍在宿主，仍不收敛 | 在边界之内 |
| Intel Mac | 宿主那条路仍能用 | 没有退路 |
| 运行时 | microsandbox（Apache-2.0，自托管） | Docker Sandboxes（`sbx`，需 Docker 登录） |
| 分发 | openheron 自带或要求装 `msb` | 一个 registry 产物加 `sbx` |

★ 两者不互斥，而且差别正是重点：microsandbox 那条是**更窄**的边界，
openheron 不挪任何东西就能采纳；这一条是**更宽**的边界，代价是零代码。

若两条都做，自然的组合是：**Kit 是外层边界，里面就不需要 microsandbox 了** ——
沙箱已经提供了 `execute` 工具本来打算去要的那层隔离。
一条命令路径上叠两层隔离，多了一层。

---

## 4. 决策

### 4.1 已定

这几条定了；`sandbox-kit/` 里的 Kit 就是照它们建的。

1. **初始化的载体**。一个 `lifecycle@1` 的 **startup hook**，
   加上入口脚本里对同一条命令的重复（§2.5）。之所以用两个载体而不是一个：
   `openheron init` 是幂等的，这份冗余的代价是一次空操作，
   却去掉了"hook 相对入口脚本的位置"这个依赖。**不改 Go。**
2. **状态放哪**。放在**挂载上**（§2.9）：数据目录保持默认的
   `<工作区>/.openheron/state`，于是对话能挺过沙箱的停止、重建，甚至移除，
   而且用户读得到。**不声明 `volume@1`**；
   `DATA_DIR` 留给想要另一种安排的用户。
3. **provider 是 openheron 自己的；凭据用一条命令绑定。** provider ——
   `openheron init` 写哪个文件 —— 由工厂模板决定，不是参数（§2.3）。
   凭据在宿主上用 `sbx secret set-custom … --placeholder proxy-managed` 绑定，
   这让宿主那边的占位符与运行时注入的是同一个字符串：容器里是哨兵值，
   密钥不进 VM，用户只跑一条命令（§2.8）。
   Kit 保留 `credential@1` 声明，所以凭据的用途仍在描述文件里 ——
   也就仍在权限面里（§2.14）。
4. **出网。** 这个 Kit **不限制** agent 能够到什么地方：运行阶段放行 `*`（§2.7）。
   边界是 microVM；宿主的策略与组织的规则才是旋钮，
   而它们在执行顺序上位于 Kit 之上。
   一个限制出网的 Kit 买到的是摩擦而不是安全 —— 而摩擦落在 agent 身上。
5. **端口要声明，宿主侧归运行时。** workload 为容器端口 `8080` 声明
   `com.docker.sandbox/port@1`（§2.5）。运行时在启动时把它发布到一个临时的
   回环端口上；用户从 `sbx ports` 里读端口。Kit 不能钉死宿主端口，
   也不需要。**family 留在默认值**，因为运行时还没实现 `dual`，
   而被拒的描述文件意味着沙箱根本起不来。
6. **会话把服务拉起来，能力把它留住。** 入口脚本就是 agent 的启动命令，
   所以 detached 的 `sbx run` 只会得到一个配好的、哑的沙箱（§2.5）。
   靠 `long-running@1`，客户端之后分离也不会把服务一起带走。

### 4.2 仍未定

这些是 owner 的决定。§6 实测过的两个问题已有结论：工作区路径**确实**被注入，
转发层**确实**不缓冲。

1. **发布的 registry**。openheron 是把 Kit 发到自己控制的 registry，
   还是每个用户从源码树自己构建？
2. **skills 目录**。`agentkit/build/snapshot.go:52-54` 把它加为第二个根。
   它在 VM 里该放哪 —— 工作区，还是 volume？
3. **`sbx` 版本下限**。`0.42.0` 或更新 —— 因为已公开逃逸的官方缓解办法
   是 openheron 用不了的那种模式（§2.14）。
   是由 Kit 声明这个下限，还是由安装器去检查？

---

## 5. 这件事要怎么验收

一个不是单元测试的边界，只能靠行为验收。

1. **逃逸测试**。一次读 `~/.ssh` 并往项目根之外写的回合必须失败，
   而且失败要能在工具输出里看见。
2. **读路径测试**。★ microsandbox 那条路通不过的那一条：
   一次向项目根之外的文件要 `read_file` 的回合必须失败。
   若它成功了，边界就不在本文档所说的位置。
3. **SSE 测试**。一个回合必须**边发生边**流到浏览器，
   而不是回合结束时一次性涌出（§2.10）。
   这条验收标准最可能第一个失败，而且没法对着 E2E 的假后端查 ——
   那个后端前面没有真的转发层。
4. **网络测试**。一条去打开允许列表之外主机 socket 的命令必须失败。
5. **状态测试**。重启沙箱：会话必须还在。
   移除沙箱：在数据目录位于挂载上时（§2.9），会话必须还在，
   而且文件必须能被宿主上的用户读到。
6. **降级测试**。凭据没提供时，沙箱必须照样起来，
   模型调用必须以明确的 401 失败 —— 而不是崩掉。
7. **凭据测试**。真密钥在沙箱里必须哪儿都不出现 ——
   环境里没有、配置根里没有、进程 dump 里也没有。
   容器里只有哨兵值，而模型调用照常成功（§2.8）。
8. **代理测试**。★ 模型调用必须真的穿过沙箱代理：
   策略日志里要看到它走在代理路径上，而容器里的环境变量
   装的是哨兵值而不是真密钥。
   一个绕过代理的客户端会把哨兵值送到 API，然后收到一个 401。
   openheron 的客户端认 `HTTPS_PROXY`（`llm/common/logging.go:77`
   委派给 `http.DefaultTransport`），但这一点要被观测到，而不是被假设。
9. **初始化测试**。一个空工作区必须带着配置起来：
   初始化写出配置、服务启动、配置根就是用户在宿主上看到的那一个。
   第二次启动必须保留已有配置，不改任何东西。
   ★ 而且配置必须落在**挂载**上，而不是 VM 内部 ——
   这正是 §2.6 的全部意义，也是运行中的沙箱唯一会悄悄搞错的一件事。
10. **归属测试**。agent 在工作区里写下的文件，用户在宿主上必须读得到，
    归属也必须是用户能接受的那种（§2.14）。
11. **成本测试**。一个沙箱里的十个回合只付一次启动，不是十次。

---

## 6. 实测

★ 2026-11-04 在真实沙箱上实测：`sbx` v0.51.0、Docker Engine 29.9.2、
Linux on WSL2，Kit 由 `sandbox-kit/` 构建（二进制本地烤入 ——
它指名的那个 release 还没发布，见 §2.9）。
workload 自己的环境是从沙箱内部抓的，宿主上读的。

| 问题 | 答案 |
|---|---|
| workload 的环境带不带 `WORKSPACE_DIR`？ | **带** —— `/home/you/my-project`：宿主的路径，也是工作区挂载的路径 |
| 进程是不是从工作区启动的？ | **是** —— `PWD` 与 `OLDPWD` 都是工作区 |
| 配置是不是落在挂载上？ | **是** —— `.openheron/{agents,llm,policy,server,state}` 在宿主上，会话也在 |
| 凭据到不到模型？ | **到** —— 容器里是 `DEEPSEEK_API_KEY=proxy-managed`；代理换上真值，调用返回了 "ok" |
| 发布版配方构建得出来吗？ | **能** —— `make kit` 用 container-driver builder 产出 `openheron-kit:0.0.6`，里面的二进制自报 `v0.0.6` |
| workload 能与 credential mixin 组合吗？ | **能** —— 容器里是 `DEEPSEEK_API_KEY=proxy-managed`，配置在挂载上，模型答了 |
| 纯声明的 mixin 没有 Dockerfile 也构建得出来吗？ | **能** —— `make kit-mixin` 只靠描述文件就产出镜像 |
| 发布版 Kit 在沙箱里跑得起来吗？ | **能** —— startup hook 把配置写到挂载上，服务在自己的数据目录里建出了 state |
| 两个厂商 mixin 能组合吗？ | **能** —— 两个哨兵都在容器里，DeepSeek 那边回了 200 |
| 两个同 service 的 mixin 能组合吗？ | **不能** —— "one credential has one owner" |
| 端口转发会不会缓冲 SSE？ | **不会** —— 10.3 秒里 1339 帧，首帧 +21ms |
| agent 写出的文件归谁？ | 归宿主用户 —— VM 的 uid `1000` 就是宿主的 uid `1000`（§2.14） |
| Kit 声明了它服务的那个端口吗？ | **没有 —— 那就是缺陷本身。** §6.5 |
| 运行时会把声明的端口发布出来吗？ | **会** —— `✓ http — published localhost:32772 -> 8080/tcp`，临时的回环端口 |
| detached 起得来服务吗？ | **起不来** —— 入口脚本是 agent 的启动命令，而 `--detach` 不开会话（§2.5） |
| 服务比客户端活得久吗？ | **活得久** —— `long-running@1` 留着它；客户端走了健康检查照样回 200 |

★ **两条凭据路都实测过。** 用占位符时（`--placeholder proxy-managed`），
容器里是 `DEEPSEEK_API_KEY=proxy-managed`，代理换上真值；
用真密钥直接注入时（`--env DEEPSEEK_API_KEY=…`），容器里是密钥，
代理发现它不是占位符就原样转发。两条都打到了模型，
只有第一条把密钥挡在 VM 之外（§2.8）。

### 6.1 实测改动了本文档的哪些地方

- §2.5 与 §2.6 不再是假设：`WORKSPACE_DIR` 确实被注入，
  于是入口脚本的 `cd "${WORKSPACE_DIR:-$PWD}"` 落在挂载上，
  配置根就是 `<工作区>/.openheron`。
- §2.10 的风险没有发生：转发层不缓冲。
- §2.14 的归属担心消解了 —— 宿主上 uid `1000` 是用户本人的话，
  agent 写出的文件就是用户的文件。
- ★ 配方改了：基础镜像本来就带着装好的 CA 证书库，
  那个 `apt-get` 步骤被删掉 —— 它既多余，又是整个构建里唯一需要包镜像的地方。

### 6.2 操作层面的发现，都不在 Kit 里

- ★ **`sbx rm` 会把已发布的端口一起带走。** 重建沙箱后，
  `sbx ports` 报 "No published ports"，转发不会自己回来；
  必须重新 `sbx ports <name> --publish 8080:8080`。
  一个失效的端口映射，看起来和"workload 死了"一模一样。
- ★ **`default` builder 会把 Kit 的 manifest annotation 丢掉。**
  镜像构建成功，而它已经不是 Kit 了：描述文件 annotation 没了。
  container driver 的 builder 才保得住，所以 `make kit` 指定了它。
- ★ **那个 builder 还需要 `--load`。** 不给的话结果留在构建缓存里，
  本地没有镜像可供校验 —— `make kit-check` 会去找一个从没导出的镜像。
- ★ **凭据和它的网络规则必须一起声明。**
  只带凭据的 mixin 组合不上：运行时要 inject 域名逐字出现在 allow 列表里，
  而 workload 的裸 `*` 不满足。mixin 于是把主机名一并加上，两张 allow 列表取并集（§2.3）。
- ★ **`openheron init` 每个配置根只维护一份 provider 配置。**
  第二个厂商的 `llm/*.yaml` 由用户写，不由 Kit 的 hook 写（§2.3）。
- ★ **端口转发在 WSL2 宿主上不可靠。** 代理拨的是 bridge 网关
  （`172.17.0.1:8080`），拿到 connection refused，而 workload 是活的：
  同一个 Kit 在同一次会话的早些时候回了 1339 帧 SSE，之后就不回了，
  Kit 本身没变。重发端口有时能救回来。这是宿主侧的症状，
  不能把账记在 Kit 上。
  ★ **这一条当时是错的。** §6.5 实测了同一个症状，给出的是另一个原因：
  Kit 根本没声明 `port@1`，于是运行时没有可转发的服务。
  转发层从来不是问题所在。
- ★ **宿主需要 `/dev/kvm`**，所以用户得在 `kvm` 组里。
  开这个设备的是守护进程，不是 CLI。
- ★ **`sbx run --name` 是重启沙箱，不会用改过的 Kit 重建容器。**
  改了 Kit 要 `sbx rm` 再 `sbx run` —— 重启会悄悄留着旧镜像，
  于是探针看起来像"没跑"。
- ★ **配方指名的是一个已发布的 release**（`v0.0.6`），所以谁都能构建这个 Kit。
  上面的实测用的是本地构建的二进制 —— 因为当时配方指名的那个 release 还不存在。

### 6.3 `WORKSPACE_DIR` 是怎么来的

★ 它是**运行时注入的沙箱级环境变量** —— 不是 Kit 给的，也不是镜像给的。
三条证据：

- Kit 的 Dockerfile 没有设它，而 workload 容器里却有；
- `sbx exec` 在同一个沙箱里起的是**另一个**容器，值完全一样 ——
  所以它属于沙箱，不属于 workload；
- `sbx` 二进制里带着它为内置 agent kit 生成的 spec 模板，
  而那些模板读它的方式一模一样：`ws="${WORKSPACE_DIR:-/}"`，
  并且 hook 上声明了 `env: [WORKSPACE_DIR]`。

运行时注入的是一整批沙箱变量，不止这一个：
`SANDBOX_ID`、`SANDBOX_NAME`、`SANDBOX_VM_ID`、`SBX_AGENT`、
`MCP_GATEWAY_URL`、`MCP_SENTINEL_TOKEN_NAME`、`SBX_CRED_<SERVICE>_MODE`，
以及代理相关的那一组（`HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`、
`NODE_USE_ENV_PROXY`、`JAVA_TOOL_OPTIONS`、`SSL_CERT_FILE`）。

★ **读它有两条路，而且不对称。** workload 的环境不设限，
所以入口脚本直接 `${WORKSPACE_DIR:-$PWD}`（§2.5）。
hook 的环境是**默认拒绝**的，所以 hook 必须声明这个名字 —— `env: [WORKSPACE_DIR]` ——
而这正是内置 kit 做的事。

★ 所以这个 Kit 用的就是 Docker 自带 agent kit 的同一套机制：
一个 startup hook 把工作区路径落进 agent 自己的配置，
一个入口脚本从同一个目录启动。

### 6.4 变量的注入

★ 四条通道，彼此不等价：

| 通道 | 作用范围 | 值从哪来 |
|---|---|---|
| 镜像的 `ENV` | 该镜像的每个容器 | Dockerfile —— 静态，人人一样 |
| Kit **参数**带 `env:` | 容器，创建时 | 安装者，展开进生效描述文件 |
| `sbx run -e` / `--env-file` | 沙箱 | 宿主 —— 裸名则取宿主上的值 |
| `sbx secret set-custom --host --env` | 沙箱 | 宿主的密钥库 —— 沙箱里只看到占位符 |

实测：`sbx exec -e INJECT_TEST=custom-value` 把 `custom-value` 放进那次 exec 的进程，
`sbx run --env MY_RUN_VAR=hello-from-run` 把它放进沙箱 —— 一直待到沙箱被重建为止；
运行时自己的那批变量也出现在 workload 的环境里（§6.3）。
★ 用 `--env` 注入的值**会盖过**capability 本来要装进同一个变量的东西 ——
这正是 §2.8.2 能用同一个 Kit 跑通的原因。

★ **hook 的 `env:` 列表不是第五条通道。** hook 的环境默认拒绝，
所以声明一个名字只是**授权可见**一个已经存在的值 —— 它不会引入新值。
hook 需要自定义变量，就得从宿主拿（`-e`）再声明那个名字；
Kit 的参数对 hook 也是这样可见的。

★ **沙箱级变量与会话级变量的生命周期不同。** 创建时烘焙进去的变量跟着沙箱走；
施加在某个会话上的变量只对这个会话有效。重建沙箱会丢掉前者。


### 6.5 端口路径，实测

★ **上一轮就是在这里走偏的，而这次纠正大概是本节最有用的东西。**
上一轮把端口转发报成"在 WSL2 宿主上不可靠"：代理拨 bridge 网关，
拿到 connection refused，而同一个 Kit 在同一次会话的早些时候回了 1339 帧 SSE。
那个读法怪的是宿主。它是错的，而原因在 Kit 这一侧。

★ **Kit 根本没声明 `port@1`。** workload 在 `8080` 上服务 HTTP，却从没说出口。
没有这个能力，运行时没有可转发的服务，于是它什么都不发布；
手工 `sbx ports --publish` 只能绑一个背后没有东西的宿主套接字 ——
客户端看到的正是"连上了，然后什么也没有"：

```
HTTP/1.1 501 Not Implemented
Connection: close
Content-Length: 0
Content-Type: application/json
```

★ **端口要通，得同时满足两件事**，而第二件正是上一轮从没测过的那件：

1. **Kit 声明端口。** `com.docker.sandbox/port@1`，容器端口 `8080`（§2.5）。
   有了它，运行时自己在启动时把这个服务发布到一个临时回环端口上 ——
   不需要 `sbx ports --publish`，因为 Kit 不能钉死宿主端口。
2. **开过一次 agent 会话。** 入口脚本就是 agent 的启动命令（§2.5），
   而 `--detach` 不开会话 —— 所以 detached 的沙箱是配好的、也是哑的。
   会话一开，服务就启动；而客户端之后分离，靠 `long-running@1` 把它留住。

★ **宿主侧、在同一个已发布端口上的实测：**

| 路径 | 结果 |
|---|---|
| 宿主 shell，`127.0.0.1:32769` | **200** |
| 宿主 shell，`[::1]:32769` | **200** |
| 守护进程自己的网络命名空间，`127.0.0.1:32769` | **200** |
| 守护进程自己的命名空间，`172.17.0.1:32769` | refused |
| 宿主的 Docker bridge 网关，`172.17.0.1:32769` | **502** |

★ **最后两行不是失败，是两个不同的地址空间。** `172.17.0.1` 是守护进程所在
命名空间里的 Docker bridge 网关，而守护进程是在**它自己**的命名空间里解析
沙箱的服务地址，不是在 guest 的命名空间里。guest 自己的转发器在没有后端时
回 502 —— 客户端够到错的 `172.17.0.1` 时看到的就是这个。
已发布的回环端口才是受支持的路径，上面那三行对它的看法是一致的。

★ **运行时日志会点名那一跳**，诊断就是这么做的：

```
"msg":"TCP proxy failed to dial backend","host_address":"127.0.0.1:32768",
"container_address":"172.17.0.1:8080",
"error":"connect tcp 172.17.0.1:8080: connection was refused"
```

★ 那八条失败属于服务从未启动的那个沙箱；而真正在服务的那个沙箱**一条都没有**。
所以计数器就是诊断手段："转发层不稳定"和"绑定后面没有东西在听"，
从客户端看一模一样，只有日志能把它们分开。

### 6.6 验收跑批

★ **这份实测是脚本，不是记录稿。** `sandbox-kit/acceptance/run.sh`
把整件事跑一遍 —— 前置检查、干净的沙箱、挂载、凭据、一次模型调用、
流的形状、客户端的离去，以及从每个视角看端口 —— 任何一项不过就非零退出。
它是本节全部内容的可重复形式。

★ `sandbox-kit/acceptance/results/` 下存着两轮记录：单厂商与两个 mixin。
两轮都是 **21 项检查、21 项通过**。它们带的数字：

| | 单厂商 | 两个 mixin |
|---|---|---|
| 已发布端口 | `127.0.0.1:32775 -> 8080/tcp` | `127.0.0.1:32776 -> 8080/tcp` |
| SSE 帧数 | 772 | 933 |
| 首帧 | +15 ms | +16 ms |
| 末帧 | +7795 ms | +8615 ms |
| 帧间跨度 | 7780 ms | 8599 ms |
| 模型调用 | 答了 | 答了 |
| 客户端走掉之后 | 200 | 200 |

★ **帧间跨度才是要紧的验收标准。** 几秒的跨度配上几毫秒的首帧，
意味着帧是随着答案生成陆续到的。若跨度很短而末帧很晚，
就说明转发层把整个响应体攥在手里到最后才放出来 —— 那正是 §2.10 说的失败。

★ **干净构建是其中一环。** 记录下来的这一轮，用的是从 `develop` 全新克隆、
**自带 builder** 构建出来的 Kit，于是构建缓存是空的：105 MB 的基础镜像与
`v0.0.6` 的 release 资产都重新拉了一遍，
配方是从零跑得通的，而不是靠上一轮留在缓存里的东西。
