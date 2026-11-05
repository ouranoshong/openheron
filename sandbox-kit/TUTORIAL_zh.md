# 在沙箱里跑 openheron

写给想把 openheron agent **跑在 Docker 沙箱里**的用户。你需要的是：一个
Docker 账号、`sbx` CLI、一把模型密钥。**不需要**源码树、不需要构建、不需要
Dockerfile —— 沙箱镜像已经发布好了。

★ **你得到的是什么。** 整个 openheron 进程 —— HTTP/SSE 服务、文件工具、
shell 工具 —— 都跑在一个 microVM 里，你的宿主在边界之外。
只有你指定的那个工作区穿过了边界，而它穿过的方式，
就是宿主上的一个普通目录。

★ **你要跑的是两样东西：**

| | 是什么 |
|---|---|
| **workload** | agent 干活的那个根文件系统，加上启动契约 —— 已发布的镜像 `docker.io/ouranoshong/openheron-kit:0.0.6` |
| **mixin** | 模型凭据，以及与它配套的网络规则 |

★ 没有 mixin 的沙箱会起得来，然后每个模型调用都回 401：
凭据正是 mixin 带来的东西。每个厂商一份 mixin，用哪几份就组合哪几份。

---

## 0. 开始之前

**一个 Docker 账号。** 沙箱是 Docker 的功能：

```sh
sbx login
```

**一个虚拟化支持。** Linux 上沙箱是 microVM，所以需要 KVM：

```sh
ls -l /dev/kvm          # must exist
```

文件在、但你拿到 "permission denied"，说明你的用户不在 `kvm` 组里。
加组需要重新登录才生效：

```sh
sudo usermod -aG kvm "$USER"
```

**模型密钥。** openheron 从环境变量读密钥，但沙箱本身不该持有密钥。
在宿主上绑一次：

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ 占位符 `proxy-managed` 是有意选的：它就是运行时注入容器里的那个哨兵值。
真值留在宿主的 secret store 里，沙箱的代理在出网那一刻把它换上。
容器里自始至终只有 `DEEPSEEK_API_KEY=proxy-managed`。

★ `--value-stdin` 让密钥既不进 shell 历史，也不进进程参数。

---

## 1. 一个要做的选择：mixin 从哪来

★ **mixin 是一份声明，不是一件产品。** 它只说两件事 —— 注入哪个凭据、
哪个主机可以够到 —— 自身不带任何内容。于是它有两种形态到你手上，
**你挑其中一种就行**；运行时两种都收，行为也完全一样：

| | 已发布的镜像 | 你自己的一份文件 |
|---|---|---|
| **你拿到什么** | `docker.io/ouranoshong/openheron-kit-deepseek:0.0.6` | 一个 YAML 文件，25 行 |
| **开始用它要做什么** | 什么都不用 —— 拉下来 | 复制进一个目录 |
| **要指向自己的端点** | 等一个新镜像 | 改一行 |
| **版本** | 固定，而且是被实测过的那个 | 就是你写下的样子 |
| **你要信任谁** | 发布者 | 除了你自己没有别人 |

★ **拿镜像**：你就是要那个被实测过的版本，也没有换端点的理由。

★ **拿文件**：openheron 得连到**你自己的**端点 —— 代理、网关、
或者某个没有发布 mixin 的厂商。那样改动就是一行，也没有 release 要等。

### 1.1 镜像

什么都不用做。引用本身就是全部：

```
docker.io/ouranoshong/openheron-kit-deepseek:0.0.6
```

### 1.2 文件

这就是全部内容。存成 `openheron-deepseek.yaml`：

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

★ **把它放进它自己专属的目录** —— 目录名就是文件名的词干：

```sh
mkdir -p ~/openheron-deepseek
# save the file above as ~/openheron-deepseek/openheron-deepseek.yaml
```

★ **运行时坚持的三件事**，都是实测的：

- **要目录，不要文件。** `--kit ~/mixin.yaml` 会被拒：
  `artifact zip "./mixin.yaml": zip: not a valid zip file` ——
  那个路径被当成了压缩包。
- **目录名就是文件名的词干。** `openheron-deepseek/` 里放
  `openheron-deepseek.yaml`。
- **v3 的 mixin 不是 ZIP。** 把目录打包会被拒：
  `artifact: spec.yaml (or spec.yml) is required` —— 那是更老的 kit 语法，
  一个 kit 是一个 `spec.yaml` 加一个可选的 `files/` 目录。

★ **为什么网络规则和凭据在同一个文件里。** 运行时要每个 inject 域名
**逐字**出现在同阶段的 allow 列表里，而 workload 自己那条规则是 `*`，
它覆盖所有域名却不满足这条检查 —— 所以 mixin 把主机名写实。
两张 allow 列表按阶段取并集。

★ **`service` 名是第二个厂商能成立的原因。** 凭据在每个 (service, phase)
上只有一个主人，所以两个厂商要两个 service 名。
两个都声称 `deepseek` 的 mixin 合不到一起。

---

## 2. 起一个沙箱

用已发布的 mixin：

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:0.0.6 \
    --name openheron ~/my-project
```

用你自己的文件：

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit ~/openheron-deepseek \
    --name openheron ~/my-project
```

★ **只有 `--kit` 那一行不同。** 两种情况下 workload 都是镜像 ——
workload 带着根文件系统，所以它只能是镜像。

参数分开看：

| | |
|---|---|
| `docker.io/ouranoshong/openheron-kit:0.0.6` | workload —— 已发布的镜像，第一次用时会拉取 |
| `--kit …` | mixin —— 镜像引用或目录，你挑 |
| `~/my-project` | 工作区 —— 宿主上的一个目录，被挂进沙箱 |

★ **工作区就是宿主上的真实目录。** 指向你希望 agent 动手的项目，
或者指向一个空目录从头开始。它在沙箱内出现在同一个绝对路径上，
所以对话里提到的路径，两边说的是同一个东西。

★ **守护进程必须看得见那个路径。** 沙箱运行时把守护进程放在它自己选的地方 ——
在 Linux 上配 Docker Desktop，那是一台虚拟机 —— 而它只挂载自己够得到的部分。
够不到的路径会在沙箱起之前就被拒：

```
error: cannot create sandbox: request failed: 400 Bad Request: workspace "/tmp/work" does not exist
```

★ 你的家目录是稳妥答案。它下面的项目够得到；`/tmp` 通常够不到。

你会看到 —— 用文件形态时，唯一的区别是 mixin 从哪来：

```
  ○ image — pulling…
      0a096e869e28 downloaded  509.3 MB
  ✓ image — ready
  ✓ assembled kit image (21 layers)
    setting 1 environment variable
      +DEEPSEEK_API_KEY
  ✓ http — published localhost:32772 -> 8080/tcp

  ✓ Created sandbox openheron
  ○ openheron — starting in sandbox openheron…
      workspace /home/you/my-project
```

★ **`http — published localhost:32772 -> 8080/tcp` 是运行时在告诉你端口好了。**
workload 声明了自己服务的端口，运行时在启动时把它发布到一个临时回环端口上。
这个端口不由你指定 —— 两个沙箱要同一个会撞。

★ **终端会一直占着。** workload 的入口脚本就是服务本身，
而会话是把它拉起来的东西。所以你会在这里看到服务的日志行。

---

## 3. 打开它

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32772       8080           tcp
::1         32772       8080           tcp
```

浏览器打开 **http://127.0.0.1:32772**。那就是完整的 openheron 界面：
对话、agents、设置编辑器。网页界面和 API 共用这个端口，
所以没有什么要配的。

★ 问它点什么。回答是边生成边流回来的 —— 你会看到字一个个出现，
而不是最后一次性给出全文。

---

## 4. 让它一直跑着

★ **你可以关掉终端，服务照样在服务。** 按 `Ctrl-\` 离开会话，
沙箱继续运行：

```
^\
```

沙箱还在，端口也还发布着。终端窗口可以关掉；
下次要用的时候，见第 6 步。

★ 没有 `long-running@1` 的话，沙箱会在最后一个客户端断开时就停掉。
workload 声明了它，所以这个服务是个后台 workload，而不是一个会话。

---

## 5. openheron 自己写下的配置

★ **第一次启动时，openheron 会在工作区种下自己的配置** ——
是启动 hook 干的，在服务起来之前就做完了：

```
~/my-project/.openheron/
  agents/main.md      the entry agent
  agents/task.md      the executor agent
  llm/deepseek.yaml   the model profile
  policy/             permissions and limits
  server/             the server's own settings
  state/              the sessions
```

★ **那些都是你的文件。** 它们在你的挂载上，明明白白摆在那儿，
openheron 会随着它们的改动重新读取。你最常改的是模型档案 ——
`llm/deepseek.yaml` 的内容是这样的：

```yaml
# ★ Factory template: DeepSeek (an OpenAI-compatible endpoint).
#
# ★ The endpoint lives in the configuration (the code hardcodes nothing) — take it
# from the vendor's docs, **path included**.
type: openai_compat
base_url: https://api.deepseek.com

# ★ api_key holds the **name of an environment variable**, rendered as ${NAME}.
api_key: ${DEEPSEEK_API_KEY}

timeout: 5m

models:
  - id: deepseek-flash
    context_window: 131072

    # Tier vocabulary: tier name → the field this endpoint sends it as.
    # An agent only writes `reasoning: high`; it does not need to know what shape
    # "high" has at this vendor.
    #
    # ★ This table is **not optional**: a tier the endpoint does not declare is
    #   **silently ignored** — an agent writes `reasoning: high` while the model
    #   has no "high", which means "no thinking at all", with no error and no
    #   message. Declaring it is what makes `high` / `low` actually take effect.
    reasoning:
      off:  { reasoning_effort: none, thinking: { type: disabled } }
      low:  { reasoning_effort: low,  thinking: { type: enabled } }
      high: { reasoning_effort: high, thinking: { type: enabled } }
      max:  { reasoning_effort: max, thinking: { type: enabled } }
    reasoning_default: off                        # used when the agent writes no reasoning
```

★ **要把它指到别处时，真正要紧的是两行：**

- `base_url` —— 厂商的端点，**带路径**，就照他们文档写的抄。
- `api_key` —— 环境变量的**名字**，写成 `${NAME}`。字面量密钥会被拒：
  这份配置是打算被提交进版本库的。

★ **`agents/main.md` 与 `agents/task.md`** 是两个 agent：入口那个
理解需求、拆任务、委派、验收；执行那个真正动手。
它们就是普通 Markdown，里面写着指令。

★ **`policy/permissions.yaml`** 决定哪些工具要先问一声：

```yaml
# ★ There are only two columns, ask and deny — **any tool not named is allowed**.
#   Name the tools that really have consequences here: ask (a nod every time) or
#   deny (unavailable).
ask:   [write_file, edit_file, execute, kb_index]
deny:  []

# ★ The outer deny takes precedence over each agent's own disabled_tools — that
#   is the one guarantee a sub-agent cannot relax. To narrow down **one**
#   agent, use its own disabled_tools.
```

★ **`server/config.yaml`** 是进程自己的设置 —— 监听地址、数据目录、
重试次数、agent 深度上限。它**不**热重载：它配置的是进程本身，
所以改了要重启。

★ **会话在 `state/` 里**，也在挂载上，所以停掉、重建甚至删掉沙箱之后，
对话还在。

---

## 6. 下次再来

```sh
sbx run --name openheron
```

★ **不带路径，也不带 `--kit`。** 沙箱两样都记得。
传路径会被 CLI 当成要运行的 agent，而 `--kit` 会被拒：

```
error: sandbox 'openheron' already exists; --kit can only be used when creating a new sandbox
  try: sbx run --name openheron
```

★ 如果服务已经在跑，这次启动绑不上端口，会报 —— 视它走到哪一步，有两种说法：

```
error: agent exited with code 2
```

```
error: inspect exec: context deadline exceeded
```

★ **两种都不是失败。** 你要的那个服务还在跑，还在已发布端口上服务。
这条消息说的是**第二份**服务，不是第一份。

---

## 7. 换一个厂商

★ **再加一份 mixin。** 两种形态都行，组合方式也一样 ——
所以从你开始用的那种接着用就行。

用镜像：

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:0.0.6 \
    --kit docker.io/ouranoshong/openheron-kit-openai:0.0.6 \
    --name openheron ~/my-project
```

用文件 —— 第二份就是第 1.2 节那个 YAML，把 `deepseek` 换成 `openai`：

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit ~/openheron-deepseek \
    --kit ~/openheron-openai \
    --name openheron ~/my-project
```

★ **还要在起沙箱之前把那个厂商的密钥绑上：**

```sh
printf '%s' "$OPENAI_API_KEY" | sbx secret set-custom \
    --host api.openai.com --env OPENAI_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ 两个密钥都以哨兵值进容器，各带各的网络规则。
而 **provider 配置** 是你自己的事：`openheron init` 每个配置根只维护一份
provider 配置，所以第二个厂商的 `llm/*.yaml` 由你在挂载上写。

---

## 8. 收工

```sh
sbx rm openheron
```

★ 沙箱、它的容器、它发布的端口一起消失。工作区留在你宿主上 ——
`.openheron/` 也在 —— 所以下一次跑会接着这次的状态。

---

## 在一个真项目上试试

★ **一件能干活的事，就是整个演示。** 仓库里带了一个：
[`examples/demo-project`](examples/demo-project) 是一个带 bug 的小 Go 程序 ——
`make test` 是失败的，而修好它只需要一个字符。

★ **在仓库根目录跑**，下面那些路径才解析得到。

用已发布的镜像：

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:0.0.6 \
    --name openheron sandbox-kit/examples/demo-project
```

★ **或者从源码树**，运行时直接用磁盘上的文件把两个 Kit 组装出来：

```sh
make kit    # once: it builds the binary and copies it beside the recipe

sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/examples/demo-project/openheron-deepseek \
    --name openheron sandbox-kit/examples/demo-project
```

★ 第二种是你**改** Kit 时该用的那条：运行时拿你刚编辑过的文件重新构建，
中间没有镜像、也没有 registry。它需要先跑一次 `make kit` ——
因为正是那一步把二进制复制到了构建够得着的配方旁边。
第一种是用户走的那条，除了 `sbx` 什么都不需要。

让它修好那个失败的测试。agent 会读文件、跑 `make test`、
**在动手改之前先问你** —— 批了它 —— 然后改动落在你宿主上，
因为工作区就是你宿主上的一个目录。

★ 这个项目**不带** `.openheron/`：第一次启动时 `openheron init` 把整份配置
写出来。而**自带**了 agent 或策略的项目会保留它们 ——
`init` 写缺的、留有的，所以**你随项目带上的文件说了算**，
而你留下的那些，第一次启动时补齐。

---

## 出了岔子

| 你看到 | 它的意思 |
|---|---|
| `error: resolve kits: kit "…": its source is not in your allowlist` | 这个 Kit 不在 `docker.io/` 上 —— 需要管理员放行它的来源 |
| `error: … fetch manifest: … http: server gave HTTP response to HTTPS client` | 那个 registry 走的是明文 HTTP；它必须提供 HTTPS |
| `artifact zip "./mixin.yaml": zip: not a valid zip file` | mixin 的 `--kit` 要的是**目录**，不是文件 |
| `artifact: spec.yaml (or spec.yml) is required` | v3 mixin 的 ZIP 会被拒 —— 那是更老的 kit 语法 |
| `error: sandbox '…' already exists; --kit can only be used when creating a new sandbox` | 你给已有沙箱传了 `--kit` —— 去掉它，或者先 `sbx rm` |
| `error: agent exited with code 2` | 服务已经在跑了；你要的那份还活着 |
| `error: resolve kits: kit "…": artifact: spec.yaml (or spec.yml) is required` | 你把路径放到了 agent 的位置上；`sbx run --name <name>` 不带路径 |
| 浏览器显示 "connection refused" | 端口变了 —— 再 `sbx ports <name>` 一次，或者沙箱停了 |
| 沙箱起来了，但端口上没人应答 | `openheron init` **一个字都没写** —— 它拒绝写下一个加载不了的配置，
所以一个坏掉的 agent 文件会让服务完全没有配置。会话日志里写着是哪个文件坏了 |
| 重启之后端口没了 | `sbx rm` 会把已发布的端口一起带走；下次启动运行时重新发布 |
| 模型调用报 401 | 密钥压根没绑 —— 重做第 0 步 |
| 模型调用报连接错误 | 凭据的网络规则没带上 —— mixin 里带着它，检查一下有没有传 mixin |

---

## 还该知道的一件事

★ **边界是沙箱，不是 allow 列表。** workload 不限制 agent 能够到什么地方 ——
隔离它的是 microVM，管出网的是宿主自己的策略（`sbx policy`）。
想收窄就在宿主上收窄，别去改 Kit。

---

★ **想自己构建？** 镜像是发布好的，但源码在仓库里：
[`BUILDING.md`](BUILDING.md) 讲怎么从源码构建、怎么改描述文件、
怎么发布新版本。[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md)
是它背后的设计，而它依赖的每一条假设都是实测出来的。
