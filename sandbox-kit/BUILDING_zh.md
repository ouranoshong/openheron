# 构建与发布 openheron 沙箱 Kit

写给要**改**这个 Kit 而不是用它的人。如果你想**用** openheron 跑沙箱，
请看 [`TUTORIAL_zh.md`](TUTORIAL_zh.md)（英文版是 [`TUTORIAL.md`](TUTORIAL.md)）——
它不需要源码树，也不需要构建。

★ **这里放着什么**

```
sandbox-kit/
  openheron/                    the workload
    openheron.yaml              the descriptor: args, capabilities, the startup hook
    openheron.dockerfile        the content recipe: the base image and the binary
    entrypoint.sh               the launch script
  openheron-deepseek/           a mixin: the DeepSeek credential and its network rule
    openheron-deepseek.yaml
  openheron-openai/             a mixin: the same, for OpenAI
    openheron-openai.yaml
  acceptance/run.sh             the acceptance run
```

★ **一个 Kit 就是一个 OCI 镜像。** 描述文件里的 `capabilities` 是它的声明，
镜像的层是它的内容。workload 带着根文件系统，mixin 只带声明、不带内容。

---

## 1. 前置条件

```sh
sbx --version      # 0.42.0 or later
docker --version
bun --version      # the frontend toolchain: make kit runs make build
```

★ 版本下限不是装饰：0.42.0 修了 `CVE-2026-77179` 与 `CVE-2026-79994`
两个 guest-to-host 逃逸，而 Docker 记录的缓解办法是 openheron 用不了的那种模式
（设计的 §2.14）。

★ **要一个自带的 builder。** 构建需要 container driver 的 buildx builder，
`make kit` 会在缺的时候建一个：

```sh
docker buildx inspect kit-builder >/dev/null 2>&1 || \
  docker buildx create --name kit-builder --driver docker-container --bootstrap
```

★ **为什么不用默认 builder。** `docker` driver 配经典镜像存储会丢掉
"让一个镜像成为 Kit"所需的 manifest annotation —— 镜像构建成功，
而它已经不是 Kit 了。container driver 的 builder 才保得住。

---

## 2. 构建

```sh
make kit         # the workload: openheron-kit:<version>
make kit-mixin   # every mixin: openheron-kit-<vendor>:<version>
```

★ **`<version>` 就是 Makefile 里的 `KIT_VERSION`** —— 版本只住在那一处。
构建把它盖进描述文件与镜像 tag，所以本地拿到的镜像就是发布出去的那个。

★ 两者都会导出到本地 docker store。不导出的话本地就没有镜像可供校验，
`make kit-check` 会去找一个从没导出的镜像。

★ **二进制是在宿主上构建的，不是在镜像里。** `make kit` 先跑
`make build` —— 前端、嵌进二进制的界面、这个仓库自己的代码 ——
再把产物复制到配方旁边：

```dockerfile
COPY openheron /usr/local/bin/openheron
RUN chmod 0755 /usr/local/bin/openheron
```

★ **两个后果。** 这个 Kit 带的是**当前源码**，而不是某个 release：
不必等 release，镜像里的二进制自报的是 `git describe` 而不是 release tag。
而且构建需要**前端工具链** —— `make build` 会跑 vite —— 所以构建一个 Kit
比"有 docker 就行"要多一些东西。

★ **二进制被复制到配方旁边**，是因为构建上下文够不到它被给到的目录之外：
仓库的 `bin/openheron` 从 Kit 自己的目录出发够不着，
而这次复制正是 `sbx run ./sandbox-kit/openheron` 也能从这里构建的原因。

★ **那个二进制是动态链接的**，而发布出来的二进制是静态的
（`CGO_ENABLED=0`）。基础镜像是 Ubuntu，所以它要的 libc 在里面；
代价是二进制更大，而且这个 Kit 绑在基础镜像的 libc 上。

---

## 3. 校验

```sh
make kit-check   # kit-tck validate against the specification
```

★ `kit-tck` 是规范仓库里的那套一致性测试。它读镜像，
所以需要第 2 步导出的本地镜像。

---

## 4. 从源码树跑

```sh
sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --name openheron ~/my-project
```

★ 本地目录就是一个合法的 Kit 引用，运行时按需构建它 ——
所以这条路径根本不需要先 `make kit`。这是发布之前验证描述文件改动的办法。

★ **`--kit` 在这里接的是目录，不是镜像。** 一个 mixin 目录里只有一个 YAML 文件、
别无他物；运行时把这份纯声明构建成镜像。

★ **会话才是把服务拉起来的东西。** `--detach` 建起沙箱、跑完启动 hook，
然后把服务留在下面：入口脚本是 agent 的启动命令，没有会话就没人拉起它。
把会话开起来，看着服务起来，再从它分离 —— `long-running@1` 会让它之后继续跑着。

---

## 5. 发布

```sh
make kit-push kit-push-mixin
```

★ **mixin 有自己的仓库，而且必须如此**：一个 Kit 由它的引用来标识，
而运行时是分别解析集合里每个引用的。只发布 workload 而不发 mixin，
用户就够不到模型 —— Kit 跑得起来，然后每个调用都回 401。

★ 三张镜像落在 Docker Hub 的 `ouranoshong/` 名下：

```
docker.io/ouranoshong/openheron-kit:latest
docker.io/ouranoshong/openheron-kit-deepseek:latest
docker.io/ouranoshong/openheron-kit-openai:latest
```

★ **仓库得先存在。** Docker Hub 的 API 可以建：

```sh
curl -s -X POST "https://hub.docker.com/v2/repositories/ouranoshong/" \
  -H "Authorization: JWT $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"openheron-kit","is_private":false,"namespace":"ouranoshong"}'
```

### 在 CI 里

★ **打 tag 就发布 Kit。** [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)
里的 `kit` job 构建 workload 与每一份 mixin 并推送，
它只在 `v*` tag 上跑，别的什么都不跑：

```yaml
needs: [go, web, e2e, secrets]
if: startsWith(github.ref, 'refs/tags/v') || inputs.tag != ''
```

★ **它等的是 release 等的那同一批检查**，包括密钥扫描：
红色提交上的 tag 不发布任何镜像，持有密钥的提交上的 tag 也不发布。

★ **它需要两个 secret**：`DOCKERHUB_USERNAME` 与 `DOCKERHUB_TOKEN`。
那个 token 是 Docker Hub 的访问令牌 ——  个人访问令牌或组织可读写令牌 ——
不是账号密码。

★ **镜像的 tag 不带 `v`**：release 是 `v0.1.0`，Kit 是 `0.1.0`，
所以指南里那个引用就是 tag。版本同时进到**已发布的描述文件**里 ——
因为 `make kit` 是在 Kit 目录的一份副本里构建的、版本写在那份副本上，
工作区永远不会被改脏。

★ **这个 Kit 是单架构的。** 二进制在 runner 上构建，所以镜像就是 runner 的
架构 —— GitHub 的 runner 是 `linux/amd64`。多架构的 Kit 需要交叉编译的二进制，
而现在这个是动态链接的。

★ **tag 就是版本，而版本就是 release。** Kit 的 `version` 参数是它安装的
release tag，`make kit-push` 用同一个版本给镜像打 tag。
一个指向没人发布过的 release 的 Kit，就是没人构建得出来的 Kit ——
所以先发 release，再推指向它的 Kit。

★ **workload 推得慢，mixin 瞬间就完。** workload 是沙箱模板加上那个二进制；
mixin 只是声明。

---

## 6. 改描述文件

`sandbox-kit/openheron/openheron.yaml` 就是契约。要紧的是这几处：

```yaml
args:
  version:
    default: "<version>"
    pattern: '^[0-9]+(\.[0-9A-Za-z-]+)*$'
    description: version label for this build of the Kit
    buildArg: OPENHERON_VERSION

provides: ["openheron@${{ kit.args.version }}"]
version: "${{ kit.args.version }}"

capabilities:
  - type: com.docker.sandbox/port@1
    config:
      name: http
      container: 8080
      transport: tcp
```

★ **这段是抄的，所以它会漂移 —— 文件才是准的。** 两者不一致时以描述文件为准；
一份悄悄过期的抄本比没有抄本更糟，因为它读起来像是当前的。

★ **`buildArg` 是版本抵达配方的路径。** 配方读 `${OPENHERON_VERSION}`，
而描述文件里的 `buildArg` 就是把参数的值放到那儿的东西。
手工 `docker buildx build -f openheron.dockerfile` 会绕过这次注入，
然后下载 404。

★ **`port@1` 不是可选项。** 要对外服务的 workload 必须声明它服务的端口：
没有这个能力，运行时没有可转发的服务，而手工 `sbx ports --publish`
只能绑一个背后没有东西的宿主套接字 —— 客户端看到的就是连上又被关掉。

★ **family 留在默认值**，而那就是实话：openheron 监听 `[::]:8080`，
那是双栈套接字，IPv4 连接也进得去，所以客户端够到发布的 IPv4 绑定就够到了服务。
声明 `dual` 说的是同一件事，而运行时拒了它：

```
error: resolve kits: kit "./sandbox-kit/openheron": port 8080: family "dual"
  is not supported yet; omit it
```

★ **被拒的描述文件意味着沙箱根本起不来**，所以声明运行时今天实现得了的那一份。

★ **mixin 把网络规则和凭据带在一起。** 运行时要每个 inject 域名**逐字**出现在
同阶段的 allow 列表里；workload 的裸 `*` 覆盖所有域名却不满足这条检查。
两张 allow 列表按阶段取并集。

---

## 7. 验收跑批

```sh
sandbox-kit/acceptance/run.sh
```

★ 它把整件事跑一遍 —— 前置检查、干净的沙箱、挂载上的配置、凭据、
一次真实模型调用、流的形状、客户端的离去，以及从每个视角看端口 ——
任何一项不过就非零退出。它跑出来的数字在
[`docs/sandbox-kit-design_zh.md`](../docs/sandbox-kit-design_zh.md) §6.6
（英文版是 [`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md)）。

★ **跑批的那份记录不进版本库。** 它是运行产物：绝对路径、临时端口、
帧数、模型自己的话 —— 每重跑一次都会与文档引用的数字对不上。
`--out` 把它写到你指定的地方。

---

## 构建出问题的时候

| 症状 | 原因 |
|---|---|
| `kit-tck` 说找不到可校验的镜像 | 构建没有导出 —— `--load` 是必需的，它就在配方里 |
| 镜像构建出来了，但运行时不认它是 Kit | manifest annotation 没了 —— 用 container driver 的 builder |
| 构建时下载 404 | 那个版本指向一个从没发布过的 release |
| 构建时报 `openheron: not found` | 二进制没被复制进去 —— `make kit` 会做这件事，
所以用 `make kit` 构建，而不是直接调 buildx |
| `family "dual" is not supported yet` | 运行时还没实现它；把字段去掉 |
| `spec.yaml (or spec.yml) is required` | `sbx kit pack` 是 **v1/v2** 的产物语法；v3 的 Kit 以 OCI 镜像或目录流传 |
| `KVM error: Permission denied` | 服务那个 socket 的守护进程不是有 `/dev/kvm` 权限的那一个 —— 见设计的 §6.2 |

---

这一切背后的设计，以及它依赖的每一条假设，都在
[`docs/sandbox-kit-design_zh.md`](../docs/sandbox-kit-design_zh.md) 里。
