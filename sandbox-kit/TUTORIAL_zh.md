# 在 Docker Sandbox 里使用 openheron

一份给普通用户的分步指南：在沙箱里跑 openheron，适合你不希望 agent
碰到本机文件的那种场合。

★ **它给你的是什么。** 整个 openheron 进程 —— HTTP/SSE 服务、文件工具、
shell 工具 —— 都跑在一个 microVM 里，你的宿主在边界之外。
只有你指定的那个工作区穿过了边界，而它穿过的方式，
就是宿主上的一个普通目录。

★ **它不给你什么。** 沙箱不是一个登录 shell。你在浏览器里打开服务，
和你在本机上打开它一模一样。

---

## 0. 开始之前

你需要三样东西。

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

**模型凭据。** openheron 从环境变量读密钥，但沙箱本身不该持有密钥。
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

## 1. 拿到 Kit

★ **如果 openheron 是从 release 装的，Kit 就在源码树里。**
它在本仓库的 `sandbox-kit/` 目录下：

```
sandbox-kit/
  openheron/           the workload — the server itself
  openheron-deepseek/  a mixin — the DeepSeek credential
  openheron-openai/    a mixin — the same, for OpenAI
```

你不必自己构建。`sbx run` 需要时会自己构建并缓存。
只有想提前把镜像备好时才需要自己构建：

```sh
make kit         # the workload
make kit-mixin   # every mixin
```

★ 两者都用自带的 buildx builder，并且都导出到本地 docker store。
默认 builder 会把"让一个镜像成为 Kit"所需的 manifest annotation 丢掉，
所以 `make kit` 指定了 container driver 的 builder。

---

## 2. 起一个沙箱

```sh
sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --name openheron ~/my-project
```

三个参数分开看：

| | |
|---|---|
| `./sandbox-kit/openheron` | workload —— agent 干活的那个根文件系统 |
| `--kit ./sandbox-kit/openheron-deepseek` | mixin —— 凭据，以及与它配套的网络规则 |
| `~/my-project` | 工作区 —— 宿主上的一个目录，被挂进沙箱 |

★ **工作区就是宿主上的真实目录。** 指向你希望 agent 动手的项目，
或者指向一个空目录从头开始。它在沙箱内出现在同一个绝对路径上，
所以对话里提到的路径，两边说的是同一个东西。

你会看到：

```
  ✓ http — published localhost:32777 -> 8080/tcp
  ✓ Created sandbox openheron
  ○ openheron — starting in sandbox openheron…
      workspace /home/you/my-project
```

★ **`http — published localhost:32777 -> 8080/tcp` 是运行时在告诉你端口好了。**
Kit 声明了自己服务的端口，运行时在启动时把它发布到一个临时回环端口上。
这个端口不由你指定 —— 两个 Kit 要同一个会撞。

★ **终端会一直占着。** Kit 的入口脚本就是服务本身，而会话是把它拉起来的东西。
所以你会在这里看到服务的日志行。

---

## 3. 打开它

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32777       8080           tcp
::1         32777       8080           tcp
```

浏览器打开 **http://127.0.0.1:32777**。那就是完整的 openheron 界面：
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
Kit 声明了它，所以这个服务是个后台 workload，而不是一个会话。

---

## 5. 干活

agent 读写的每一样东西都在沙箱里 —— 但工作区是你的目录，
所以你能在宿主上盯着它：

```sh
ls ~/my-project
```

★ 第一次启动时，openheron 会在那里种下自己的配置：

```
~/my-project/.openheron/
  agents/main.md      the entry agent
  agents/task.md      the executor agent
  llm/deepseek.yaml   the model profile
  policy/             permissions and limits
  server/             the server's own settings
  state/              the sessions
```

★ **那些都是你的文件。** 用你自己的编辑器改 `llm/deepseek.yaml`，
换模型或换 base URL，openheron 会自己发现。
配置没有藏在沙箱里 —— 它在你的挂载上，明明白白摆在那儿。

★ **会话也留着。** `state/` 同样在挂载上，所以停掉再重建沙箱之后，
对话还在。

---

## 6. 下次再来

```sh
sbx run --name openheron
```

★ **不带路径，也不带 `--kit`。** 沙箱两样都记得。
传路径会被 CLI 当成要运行的 agent，而 `--kit` 会被拒绝：

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

加上 mixin，并把那个密钥绑上：

```sh
printf '%s' "$OPENAI_API_KEY" | sbx secret set-custom \
    --host api.openai.com --env OPENAI_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --kit ./sandbox-kit/openheron-openai \
    --name openheron ~/my-project
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

## 出了岔子

| 你看到 | 它的意思 |
|---|---|
| `error: sandbox '…' already exists; --kit can only be used when creating a new sandbox` | 你给已有沙箱传了 `--kit` —— 去掉它，或者先 `sbx rm` |
| `error: agent exited with code 2` | 服务已经在跑了；你要的那份还活着 |
| `error: inspect exec: context deadline exceeded` | 同一件事，从另一头看到的说法 |
| `error: resolve kits: kit "…": artifact: spec.yaml (or spec.yml) is required` | 你把路径放到了 agent 的位置上；`sbx run --name <name>` 不带路径 |
| 浏览器显示 "connection refused" | 端口变了 —— 再 `sbx ports <name>` 一次，或者沙箱停了 |
| 重启之后端口没了 | `sbx rm` 会把已发布的端口一起带走；下次启动运行时重新发布 |
| 模型调用报 401 | 密钥压根没绑 —— 重做第 0 步 |
| 模型调用报连接错误 | 凭据的网络规则没带上 —— mixin 里带着它，检查一下有没有传 mixin |

---

## 还该知道的一件事

★ **边界是沙箱，不是 allow 列表。** 这个 Kit 不限制 agent 能够到什么地方 ——
隔离它的是 microVM，管出网的是宿主自己的策略（`sbx policy`）。
想收窄就在宿主上收窄，别去改 Kit。

---

这一切背后的设计，以及它依赖的每一条假设，都在
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) 里。
这份指南的可重复版本是
[`sandbox-kit/acceptance/run.sh`](acceptance/run.sh) ——
上面每条断言在那儿都是一项检查，而检查不成立时跑批就失败。
