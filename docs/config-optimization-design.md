# 配置系统优化改造方案

> **状态：提案，未实施。**
>
> ★ 本文写的是**要改成什么样**，不是**现在是什么样**。现在的实现在
> `docs/config-design.md` §8（热更新）与 §6（配置表面）里；本文的每一条结论都从那里出发。
> 文末 §6 给出实施顺序与验收用例，§7 是已经拍板的决定与仍需拍板的问题。
>
> ★ **触发它的两件事都发生在真实使用里**，不是设想：
>
> 1. 一次 `config_write` 落在 `agents/main.md` 或 `agents/skill-expert.md` 上，
>    **子代理的运行被打断**：改动落盘了，报告回不来。换成 `edit_file` 就正常，
>    但那条路径没有修订冲突检查。
> 2. 改完配置之后，**模型判断不出自己到底改没改成功** —— 因为同一个轮次里，
>    `config_get` 读的是盘上的新内容，`config_tier` 读的是本轮运行中的旧配置，
>    而工具的文案却说「所有 `config_*` 工具读的是本轮快照」。于是它要么再写一遍，
>    要么报告「配置没生效」。
>
> 本文分三部分，**顺序即优先级**：
>
> | 部分 | 解决什么 | 阶段 |
> |---|---|---|
> | §2 工具表面的一致性 | 模型能不能判断自己改没改 | P1 |
> | §3 写入路径统一 | 改配置和改普通文件是不是同一套检查 | P1 |
> | §4 重载改造 | 一次改动为什么换掉整棵树 | P2–P4 |
> | §5 影响范围查询 | 「下一轮什么会变」可不可回答 | P2 |

---

## 1. 现状：六条事实

★ 这一节只写事实，每一条都指到代码。后面所有设计都是从这六条推出来的。

### 1.1 重载是**全局换代**，不是按需重建

- `agentkit/spec/reload.go:204` `Watch()` 轮询各热分区的指纹；变了就
  `agentkit/spec/reload.go:246` 调 `Reload()`。
- `Reload()` 走 `onChange(cfg)` → `main.go:707` `agentConfig.set()` → **`gen++`**
  （`main.go:710`）。
- `agentkit/build/snapshot.go:144` `snapshotFor(cfg, gen)`：代际变大就**重建整棵快照**。
- `agentkit/build/pool.go:31` `GetOrBuild(ctx, name, gen, fn)`：池的键是 **(名字, 代际)**。
  代际一变，**每个** agent 都重建。
- ★ 而 `agentkit/build/assemble.go:100` `cfg.SubAgents = children` ——
  重建一个 agent 会把它的子树一起重建。

⇒ **改一个 `skills/<name>/SKILL.md`，整棵 agent 树在下一轮全部重建。**

### 1.2 一次运行**不钉住自己的 agent**

- `agentkit/run/loop.go:132` `makePrepareAgent` → `agentkit/build/builder.go:181`
  `b.mgr.Current()` → 取**当前**的配置与代际。
- 恢复审批走的是同一条路：`agentkit/run/runtime.go:522` 明写「a resume is a NEW run」，
  新 run 再走一次 `makePrepareAgent`（`agentkit/run/loop.go:347` `makeGenResume` 之后）。
- ⇒ **一轮被审批打断再回来，树上已经是新代际**：被打断的那个 agent 的定义、
  档位表、甚至它自己的文件，都可能已经被换掉了。

★ 这就是「改动落盘了但报告回不来」的位置：报告是**那个 agent** 写的，
而恢复的时候它已经不是同一个 agent 了。

### 1.3 写配置有**两条路**，只有一条带检查

| 路径 | 落到哪 | 检查 |
|---|---|---|
| `config_write` | `agentkit/spec/write.go:484` `Service.Write()` | 修订检查、试装载、密钥扫描、「不许把能跑的 agent 弄丢」闸门、原子写 |
| `edit_file` / `write_file` | `agentkit/fsx` 直接写文件（`agentkit/fsx/fsx.go:728` / `:751`） | **无修订检查、无闸门** |

两条路落在**同一个文件**上。于是同一个改动有两种语义：一种带冲突检查，一种不带；
一种被闸门守着，一种把 `agents/main.md` 写坏了也只能等下次装载才发现。

★ 配置文件根在 `projectRoot` 之内，而 `projectRoot` 正是 fsx 的写入根
（`agentkit/fsx/fsx.go:229` `resolveWrite`），所以文件工具**够得着**配置根。

### 1.4 现在的重载是**内容哈希**驱动的——这一点已经对了

- `agentkit/spec/reload.go:36` `HotDirFingerprints()`：每个热分区一个指纹，
  指纹里含**每个文件的内容哈希**（`agentkit/spec/reload.go:74` `dirEntryLines`，
  哈希在 `agentkit/spec/reload.go:106` `contentHash`）。
- ★ 也就是说：**真相已经在磁盘上**，重载不是靠「谁通知了我」，而是靠「重算一遍哈希」。
  这条是下面 §4 的地基：标记只能是**优化**，不能是真相。

### 1.5 ★ 同一个轮次里，六个 `config_*` 工具**各答不同的那一边**

一轮跑在一棵树上（§1.2），而盘上可能已经是新的了。于是每个工具答哪边，
不是一个统一的约定，而是**各写各的**：

| 工具 | 答 | 出处 |
|---|---|---|
| `config_get <path>` | ★ **盘** | `agentkit/build/configtools.go:98` `svc.ReadFile` → `agentkit/spec/write.go:390` `os.ReadFile` |
| `config_get`（无）`items` / `configVersion` | ★ **盘** | `agentkit/spec/manifest.go:106` `ListFiles` + `:113` `ReadFile` |
| `config_get`（无）`effective` | **运行中** | `agentkit/build/configtools.go:385`（`cfg` 是本轮快照） |
| `config_validate` | ★ **盘** | `agentkit/spec/diag.go:125` `Check()` → `:133` `loadOnly(nil)` 重新读盘 |
| `config_notices` `deferred` / `version` / `watch` | **运行中** | `agentkit/build/configtools.go:219` |
| `config_notices` `configVersion` | ★ **盘** | 同上，`svc.Fingerprint()` |
| `config_tier` | **运行中** | `agentkit/build/configtools.go:70` `policyOf(cfg)` |
| `config_write` 的 `ConfigVersion` | ★ **盘** | `agentkit/build/configtools.go:307` |

★ **一半答盘、一半答运行中，而答案里没有哪个字段说明自己是哪一边。**
唯一带标签的是 `config_get`（无 path）的 `effective` 字段 —— 它的名字就是「运行中」。

### 1.6 ★ 而且有**两句话把这件事说反了**

```go
// agentkit/build/configtools.go:327 —— config_write 的 Note
"config_* tools in this turn still read the snapshot it started with"

// agentkit/build/configtools.go:466 —— missingAgentError 的消息
"...config_write takes effect on the next turn, and all config_* tools in a turn
 read the snapshot that turn started with."
```

**两句都是错的**：`config_get`（带 path）、`config_validate`、`config_get` 的
`items` / `configVersion` 都**读盘**（§1.5）。

★ 这两句出现在**模型判断「我到底改没改成」时会撞上的那两个答复**里，
而且它们彼此矛盾：模型 `config_get` 看到新内容，却被告知「读的是快照」——
于是它**不敢信**自己看到的内容。

★ 顺带说明：`missingAgentError`（`agentkit/build/configtools.go:459`）**本身就是为这个坑修的**，
它的注释写着「a turn that had just written the file read that as "the write failed"
and wrote it again」。解法是对的 —— **点名情形 + 给下一步**。
只是① 它只覆盖 `config_tier` 的 lookup miss；② 里面的事实陈述是错的。

⇒ 「以为没改配置」⇒ **再写一遍**，或者**报告「配置没生效」**。

### 1.7 ★ 文件工具**换不掉**

- `agentkit/build/assemble.go:31` `cfg.Backend = h.backend` —— 把 fsx 后端交给框架。
- 框架那边（eino 的 `adk/prebuilt/deep`）：`adk/prebuilt/deep/deep.go:219` 只要
  `cfg.Backend != nil` 就挂**整套** filesystem middleware
  （`adk/prebuilt/deep/deep.go:67` 的注释列了
  `read_file, write_file, edit_file, glob, grep`），**没有子集选项**。
- 而 openheron 自己的 provider **不能**占用这些名字：
  `agentkit/build/tools.go:113` `reservedToolNames` 把它们归给
  「filesystem middleware」，`agentkit/build/tools.go:178` 会对占用者报
  「tool %q is owned by %s」。

⇒ ★ **「自己实现同名的 `write_file` / `edit_file`」这条路是堵死的**：
模型会看到两个同名的工具；而要关掉框架那套，就得连 `ls` / `read_file` / `glob` / `grep`
和技能链一起关掉。

⇒ 所以「配置根内的写转交给配置服务」的**唯一落点是 backend 层** —— 而 backend
就是 openheron 自己的 `agentkit/fsx`。

---

## 2. 工具表面的一致性（最先做）

★ 为什么要先做这一部分：它决定**模型能不能判断自己改没改**。
§3 与 §4 改的是行为，这一部分改的是**模型能看见的事实**——
而模型只有看见了事实，才不会重复写、也不会误报「没生效」。

### 2.1 提案：把「答哪一边」写进**字段**，不写进散文

| 工具 | 提案 |
|---|---|
| `config_get`（带 path） | 加 `"source": "disk"` |
| `config_get`（无 path） | 已有 `effective`（运行中）；给 `items` 与 `configVersion` 标上 `"disk"` |
| `config_validate` | 加 `"source": "disk"` |
| `config_tier` | 加 `"source": "running"` |
| `config_notices` | 加 `"source": "running"`；`configVersion` 标注为盘 |
| `config_write` | 加 `runningConfigVersion`（快照的 `cfg.Version`）与 `diskConfigVersion`（盘的 `svc.Fingerprint()`） |

★ **统一用 `source`，一个词。** 现在 `version`（快照）与 `configVersion`（盘）
只差一个词 —— 那不是标注，是巧合。

★ `config_write` 的两个版本号是这一部分**最有价值的一条**：
它把「运行中那份是旧的」从**要靠推理**变成**直接可读**。
现在它的输出只有一个 `ConfigVersion`（盘），模型看不出另一半。

### 2.2 提案：把那**两句话改对**

- `config_write` 的 Note（`agentkit/build/configtools.go:327`）改成：
  > 「已落盘（revision X）。**本轮运行中的配置没有变** —— `config_tier` 与
  > `config_notices` 的 `version` 答的仍是它；`config_get` 读的是盘，所以它**已经能看到
  > 新内容**。生效的判据是**下一轮**。」
- `missingAgentError`（`agentkit/build/configtools.go:466`）里那句
  「all config_* tools in a turn read the snapshot」改成：
  > 「`config_tier` 答的是**本轮运行中**的配置；`config_get` 读的是**盘**，已经能看到
  > 这个文件的新内容。」

★ 这两句是**同一个坑的两半**：第一句让模型以为看不见，第二句让模型以为看得见的
是旧的。合起来正好制造「以为没改配置」。

### 2.3 提案：Note 分版本改

「下一轮会怎样」在三个阶段是三句话，**不能提前说后面那句**：

| 阶段 | Note 怎么说 |
|---|---|
| P1（写路径统一，重建逻辑不变） | 下一轮**整棵树**按新配置换代 |
| P2（按依赖重建） | 下一轮**按依赖**重建：只有依赖这些文件的 agent 会变 |
| P3（run 钉住自己的树） | **下一个新 run** 生效；**恢复当前审批的那一轮**沿用开始时的配置 |

★ P3 那一行是整个方案里最该说清楚的一句 —— 它就是「打断」那个症状的解法，
而模型必须能**看见**这个承诺，否则它不敢在同一轮里重试。

---

## 3. 写入路径统一：文件工具写配置根 ⇒ 转交配置服务

### 3.1 提案一句话

> **配置根里的文件，模型用文件工具改时，也要过配置服务的那五道闸门。**

★ 措辞要准：**不是「模型只有一条路能写配置根」** —— `execute` 是 shell，
`echo > .openheron/agents/main.md` 照样能绕过去（§3.6）。能说的是
「**用文件工具改配置时**会走检查」。

### 3.2 落点：fsx 的一个**窄接口**，实现由 build 层提供

★ 不能让 fsx 直接 import `spec`：`agentkit/fsx` 目前**不 import 任何 openheron 包**
（它是有意的叶子后端，见 `docs/fs-backend-design.md`），让它知道「什么是配置」
会造出 `fsx → spec` 的依赖倒置。

⇒ **fsx 自己定义接口，build 层提供实现**：

```go
// agentkit/fsx —— 它只知道自己定义的接口，不知道 spec
type ConfigGate interface {
    // OwnsPath reports whether this absolute path is a configuration file.
    OwnsPath(absPath string) bool
    // WriteConfig hands the write to the configuration service.
    // It returns the new revision.
    WriteConfig(absPath string, content []byte, baseRev string) (string, error)
}
```

`fsx.Config` 多一个字段 `ConfigGate ConfigGate`；`agentkit/fsx/fsx.go:728` `Write`
与 `:751` `Edit` 在 `resolveWrite` 之后、真正落盘之前问一次：

```
Write / Edit
  └─ resolveWrite(path)                    ← 现有的写入边界，不动
      └─ gate.OwnsPath(path) ?
           ├─ 否 → 原来的落盘路径（行为完全不变）
           └─ 是 → gate.WriteConfig(...)   ← 走 svc.Write，五道闸门
```

★ build 层是唯一同时持有 fsx 后端与 `spec.Service` 的地方
（`agentkit/build/snapshot.go:72` 造 fsx，`agentkit/build/snapshot.go:89` 的
`toolEnv` 里带着 `svc`），所以实现放在那里，依赖方向天然正确。

### 3.3 ★ 冲突语义：`edit_file` 有，`write_file` 没有

★ 这是本方案里**最需要如实说清**的一处，因为 backend 层拿不到模型手里的基线：

- eino 的 `fsapi.WriteRequest` 只有 `FilePath` + `Content`，
  `fsapi.EditRequest` 只有 `FilePath` + `OldString` + `NewString`（+ `ReplaceAll`）——
  **没有 revision 字段**（eino `adk/filesystem/backend.go:153` / `:162`）。
- 而工具是框架挂的，模型**没法给工具多传一个参数**（§1.7）。

⇒ 所以：

| 工具 | 冲突语义 |
|---|---|
| `edit_file` | ★ **有**，而且比版本比对更贴切：**OldString 必须仍在**（`agentkit/fsx/fsx.go:773`）。别人改了**别的地方**不该挡住你；改了**你动的那一段**，才该报冲突 |
| `write_file`（整篇替换） | ★ **没有**。模型没有基线可给 |

⇒ ★ 与 `config_write` 的差异要**写进工具描述**：`config_write` 有 `baseRevision`
（模型能从 `config_get` 拿到，`agentkit/build/configtools.go:111`），
文件工具没有。**这是工具能力差异，不是设计缺陷** —— 但不说清楚，模型就会以为
两边一样，然后踩坑。

### 3.4 ★ `svc.Write` 的五道闸门一个不少

转交之后走的是同一个函数（`agentkit/spec/write.go:484`），闸门自然全在：

1. **路径校验** —— `agentkit/spec/write.go:151` `validateWritable`
   （`server/` 分区不可编辑，`agentkit/spec/config.go:71`）；
2. **密钥扫描** —— `agentkit/spec/write.go:488` `checkSecretFor`；
3. **修订检查** —— `agentkit/spec/write.go:494` `checkRevision`；
4. **试装载** —— `agentkit/spec/write.go:497` `loadWith(m)`；
5. **agent 丢失闸门** —— `agentkit/spec/write.go:504` `checkNoAgentLost`；
6. **原子写** —— `agentkit/spec/write.go:512` `persist.AtomicWrite`。

★ 第 3 条在文件工具这条路上几乎恒过（基线是刚读到的 revision），
它的价值在 §3.3 已经让位给 `OldString` 语义。**它仍然留着**：
`config_write` 那条路上它是主力，两条路共用同一个函数正是转交的意义。

### 3.5 ★ `execute` 仍能绕过——这是已知边界

`execute` 是 openheron 自己的工具（`agentkit/build/execute.go:40`），它跑 shell。
shell 里的重定向能写配置根，**绕得过任何闸门**。

⇒ ★ 方案**不**试图堵这个洞（那要让 shell 对配置根只读，等于给进程套沙箱，
是另一件事）。方案做的是：把**闸门覆盖到模型用文件工具改配置的那条路**上，
并把剩下的洞**写进文档**，而不是假装没有。

### 3.6 转交之后，写配置**不再**意味着「整棵树要换」

现在：写 → 落盘 → watcher 发现 → `gen++` → **下一轮整棵树重建**。
提案之后：写 → 落盘 → **只标记**（§4.2）→ 下一轮**按依赖**重建（§4.3）。

★ 于是「写配置」的后果变得可预测：**它改的是磁盘；它什么时候影响一个 agent，
由那个 agent 的依赖指纹决定。**

---

## 4. 重载改造：标记 + 按依赖重建

### 4.1 提案一句话

> **写只标记；重建按依赖发生；一轮钉住自己开始时的那棵树。**

### 4.2 标记：写路径**不再**触发重建

- 写路径（`config_write` 与文件工具）做完闸门、落盘之后，**只做一件事**：
  把「哪些文件变了」记进一个**脏集合**。
- ★ **脏集合是缓存，不是真相。** 真相是磁盘上的内容哈希（§1.4）。
  所以脏集合丢了、重启了、清空了，**都不影响正确性** ——
  下一轮重算哈希时照样算得出变化。★ 这是「标记」这个词唯一安全的用法：
  它省掉一次全量扫描，不承担正确性。
- 脏集合放哪：**进程内**（`agentkit/build` 的 builder）。不写进 `state/`：
  它不是需要跨重启存活的事实。

### 4.3 重建的粒度：从**全局代际**换成**每个 agent 自己的依赖指纹**

代际是**全局**的，所以任何改动都会让所有 agent 失效。要按需重建，就得把
「什么变了」换成**「谁变了」**——给每个 agent 一个**自己的**指纹。

**指纹的输入清单**：

| 输入 | 为什么算进去 |
|---|---|
| `agents/<id>.md` 自己的文件 | 它的正文与 frontmatter |
| 它 `model:` 指向的那个 `llm/<provider>.yaml` | 模型、档位、超时 |
| 它依赖的 `tools/*.yaml` | 档位表（`allows:` 决定它要不要点头） |
| 它挂载的 `mcps/<server>.yaml` | 挂载即授予全部工具 |
| 它 `skills:` 列出的那些 `skills/<name>/` | 技能正文 |
| **它子 agent 的指纹**（递归） | ★ 父的 `task` 工具描述里嵌着子 agent 的名字与描述 |

⇒ **池的键从 (名字, 代际) 变成 (名字, 指纹)**（`agentkit/build/pool.go:31`）。

★ **这一条顺带解决了「改一个 skill 重建整棵树」**：技能只进它自己那几个
agent 的指纹，别人的指纹不动，池里那份 entry 继续用。

### 4.4 ★ 一轮钉住自己的 agent —— 这是修「打断」的那一条

- **问题**：一轮跑到一半被审批打断，回来时 `makePrepareAgent` 取的是**当前**代际
  （§1.2），于是它继续在一个**换过的** agent 上跑。
- **提案**：**run 记住自己开始时那棵树的指纹**；恢复时按**指纹**取回，
  不是按名字取「当前那棵」。
  - 池按 (名字, 指纹) 存 entry（`agentkit/build/pool.go:58` 现在是 `p.ents[name] = e`，
    一代换一代就把旧的丢掉了）—— 改成**多留一代**：
    `p.ents[name][fingerprint] = e`，旧的 entry 在**没有 run 引用它**之后才回收。
  - run 的行（`storage/runledger.go` 的 `Run`）或 turn handle 记下这棵树的指纹；
    `makePrepareAgent` 在恢复时拿这个指纹去要 agent。
- ⇒ **被打断的那一轮，继续用它开始时的那棵树**：报告回得来。
  而**新配置在下一轮生效**（下一轮 = 下一个新 run，不含恢复那一轮）。
- ★ 这条与 §4.3 是同一件事的两面：指纹既是「谁变了」的答案，
  也是「我是谁」的答案。有了它，恢复不必猜「当前是第几代」。

### 4.5 MCP 的工具清单变化走同一条路

现在：MCP server 的清单变了 → `main.go:727` `bump()` → **全局** `gen++`
（`agentkit/build/builder.go:107` `SetGenerationBump`）。
提案：清单变了 → **受影响 agent 的指纹变了** → 池按 §4.3 重建它们。
★ 不再需要 `bump()` 这条全局通道；**「代际」这个概念可以整体退休**。

### 4.6 提案明确**不做**的事

- ★ **不做「写完立刻重建」**：那正是现在的行为，也是打断的来源。
  写与重建解耦，是这一节全部内容的要害。
- ★ **不做「脏集合决定正确性」**：脏集合只决定「要不要重算」。
  重算永远以磁盘哈希为准（§1.4）。
- ★ **不改 `server/` 的非热重载**：它不在热分区里（`agentkit/spec/config.go:71`
  `Editable: false, hot: false`），本来就只影响进程自己的参数。

---

## 5. ★ 影响范围查询（P2 的必需功能）

★ 这是 §1.6 那个坑暴露出来的缺口，也是 §2 那一部分**答不了**的问题。

模型写完配置后真正要回答的是：**「下一轮什么会变？」**
而这个问题有两个子问题：

1. **下一轮会不会变？** —— 改了一个没人依赖的文件（比如一个不在任何 agent 的
   `tools:` 里的 `tools/foo.yaml`），**下一轮什么都不会变**。
   ★ P1 阶段**根本回答不了**：写路径不看依赖，谁也不知道这份文件被谁引用。
2. **下一轮谁会变？** —— 改了 `agents/skill-expert.md`，只有它和它的父会变。

⇒ ★ **提案：P2 必须提供一个能回答这两问的查询。** 它恰好是指纹体系的自然产物：
指纹的输入清单（§4.3）就是「谁依赖它」的答案。

⇒ **形态**：一个 `config_*` 查询（名字待定），输入一个配置文件的相对路径，
输出「依赖它的 agent 列表」与「下一轮会重建的 agent 列表」。

★ 没有它，模型永远只能靠猜，然后**再写一遍** ——
那正是 `missingAgentError` 当年治的那个循环，换了个地方复发。

---

## 6. 实施顺序与验收

### 6.1 分期

| 期 | 做什么 | 风险 |
|---|---|---|
| **P1** | §2 工具表面一致性 + §3 写入路径统一 | 小：只改写路径与答案，不动重建 |
| **P2** | §4.2 + §4.3：标记 + 每 agent 依赖指纹，池的键换成指纹；**外加 §5 的影响范围查询** | 中：重建粒度的核心 |
| **P3** | §4.4：run 钉住自己的树（多留一代 + 记住指纹） | 中：触及恢复路径 |
| **P4** | §4.5：MCP 清单变化并入指纹，全局 `bump()` 退休 | 大：代际这个概念退场 |

★ **P1 可以单独上**：它解决「`edit_file` 没有修订冲突检查」与
「模型判断不出自己改没改」这两条，不依赖后面任何一期。
而 P1 单独上线时，**「打断」还在** —— 写配置仍然会整棵树重建，所以要等到 P3。

### 6.2 验收用例（每一条都要有测试）

**P1**

- `edit_file` 改 `agents/main.md` ⇒ 走 `svc.Write` ⇒ 树装不上时**不落盘**。
- `edit_file` 改 `agents/main.md`，`OldString` 不存在 ⇒ **报错**，不是空补丁。
- `edit_file` 改一个**普通文件** ⇒ 不走服务层，行为不变。
- `write_file` 改 `agents/main.md` ⇒ 走 `svc.Write` ⇒ 密钥扫描拦得住。
- `write_file` 改 `server/config.yaml` ⇒ 被 `validateWritable` 拒（不可编辑分区）。
- `config_write` 输出含 `runningConfigVersion` 与 `diskConfigVersion`，两者不同。
- `config_get <path>` 输出含 `"source": "disk"`；`config_tier` 含 `"source": "running"`。
- `config_write` 的 Note 与 `missingAgentError` 的消息**不再**说「所有工具读快照」。

**P2**

- 写 `agents/skill-expert.md` ⇒ **skill-expert 与它的父**被重建，
  **别的 agent 不重建**（池的 miss/hit 计数可断言）。
- 写 `skills/<name>/SKILL.md` ⇒ 只有 `skills:` 里点了它的 agent 被重建。
- 写 `tools/http.yaml` ⇒ 档位表受影响的 agent 被重建。
- 写一个**没人依赖**的 `tools/foo.yaml` ⇒ **没有 agent 被重建**，且影响范围查询
  明确说「无人依赖」。

**P3**

- ★ **恢复的那一轮**：审批打断 → 落盘 → 批准 ⇒ 恢复拿到**同一个 agent 实例**
  （断言指针相同或指纹相同），报告回来。
- ★ **下一轮**才看到新配置（断言第二轮的 agent 指纹变了）。

**全部**

- 重启进程 ⇒ 脏集合为空 ⇒ 第一轮重算哈希 ⇒ 仍然正确地重建。

---

## 7. 决定

### 7.1 已经拍板的五条

| 问题 | 决定 | 理由 |
|---|---|---|
| 文件工具写配置根：转交还是拒绝？ | **转交**（§3） | 模型保留「surgical 改一行」的自由，同时拿到五道闸门 |
| 「下一轮生效」的边界：恢复那一轮算不算？ | **不算**（§4.4） | 它继续用开始时那棵树，这是修「打断」的唯一办法 |
| 给不给模型删除动作？ | **先不给** | `svc.Delete`（`agentkit/spec/write.go:762`）留在服务层；删除不可逆 |
| `server/` 要不要进指纹体系？ | **不进** | 它本来就只影响进程自身参数，改了要重启 |
| 旧 entry 留多久？ | **留到进程退出** | 不需要额外口径，池在进程内，退出即回收 |

★ 第五条让原方案里的「P5：给模型删除动作」作废。

### 7.2 仍需拍板

1. **§5 的影响范围查询叫什么、放在哪个工具上？**
   候选：`config_get` 加一个「影响范围」子参数（不新增工具），
   或者新增第七个 `config_*` 工具（要动 `agentkit/spec/decl/decl.go:379` 的常量块）。
   ★ 倾向前者：**工具面越小越好**，而且 `config_get` 已经是「问配置」的那一个。
2. **§3.3 的差异要不要写进文件工具的描述？**
   ★ 倾向写 —— 不写，模型就会以为文件工具和 `config_write` 一样有冲突检查。
3. **§2.1 的 `source` 字段用不用英文？**
   ★ 倾向用英文（`disk` / `running`）：接口面向模型，且与其它字段一致。

---

## 附录 A：本文引用的代码位置

### 配置服务（`agentkit/spec`）

| 位置 | 是什么 |
|---|---|
| `agentkit/spec/config.go:58` | `Categories` —— 各分区与它们的 `hot` 标志 |
| `agentkit/spec/config.go:71` | `server/` 分区：`Editable: false, hot: false` |
| `agentkit/spec/config.go:114` | `hotDirList()` |
| `agentkit/spec/config.go:150` | `Config.Version` |
| `agentkit/spec/config.go:395` | `Service.Root()` |
| `agentkit/spec/config.go:405` | `Service.ProjectRoot()` |
| `agentkit/spec/config.go:463` | `loadWithMany()` —— 校验（`:468`）之后写 `Version`（`:471`） |
| `agentkit/spec/reload.go:21` | `HotDirs` |
| `agentkit/spec/reload.go:36` | `HotDirFingerprints()` |
| `agentkit/spec/reload.go:74` | `dirEntryLines()` —— 指纹里含每个文件的内容哈希 |
| `agentkit/spec/reload.go:106` | `contentHash()` |
| `agentkit/spec/reload.go:163` | `NewReloader()` |
| `agentkit/spec/reload.go:174` | `Reload()` —— 失败时保留旧一代 |
| `agentkit/spec/reload.go:204` | `Watch()` |
| `agentkit/spec/reload.go:246` | watcher 里的 `Reload()` |
| `agentkit/spec/write.go:21` | `revisionLen = 8` |
| `agentkit/spec/write.go:151` | `validateWritable()` —— 路径校验 |
| `agentkit/spec/write.go:377` | `Revision()` —— 内容哈希 |
| `agentkit/spec/write.go:382` | `ReadFile()` —— **读盘** |
| `agentkit/spec/write.go:484` | `Write()` —— 五道闸门 |
| `agentkit/spec/write.go:762` | `Delete()` —— 删（无工具暴露） |
| `agentkit/spec/write.go:815` | `checkRevision()` |
| `agentkit/spec/diag.go:125` | `Check()` —— **重新读盘** |
| `agentkit/spec/manifest.go:68` | `Manifest()` |
| `agentkit/spec/decl/decl.go:379` | 六个 `config_*` 工具名的常量块 |

### 装配层（`agentkit/build`）

| 位置 | 是什么 |
|---|---|
| `agentkit/build/pool.go:31` | `GetOrBuild()` —— 池的键 |
| `agentkit/build/pool.go:58` | `p.ents[name] = e` —— 换代即丢旧 entry |
| `agentkit/build/builder.go:107` | `SetGenerationBump()` |
| `agentkit/build/builder.go:181` | `b.mgr.Current()` |
| `agentkit/build/builder.go:254` | `pool.GetOrBuild()` 的调用点 |
| `agentkit/build/assemble.go:31` | `cfg.Backend = h.backend` —— 把 fsx 交给框架 |
| `agentkit/build/assemble.go:100` | `cfg.SubAgents = children` —— 重建带子树 |
| `agentkit/build/snapshot.go:48` | `heavyDepsOf()` |
| `agentkit/build/snapshot.go:72` | `fsx.New(...)` —— 后端在这里造 |
| `agentkit/build/snapshot.go:89` | `toolEnv` —— `cfg` 与 `svc` 都在这儿 |
| `agentkit/build/configtools.go:62` | `configToolProvider.Provide()` |
| `agentkit/build/configtools.go:98` | `config_get` 读盘 |
| `agentkit/build/configtools.go:111` | `config_get` 的 `Revision` 字段 |
| `agentkit/build/configtools.go:219` | `config_notices` 的输出 —— `version` 与 `configVersion` |
| `agentkit/build/configtools.go:296` | `config_write` 调 `svc.Write` |
| `agentkit/build/configtools.go:307` | `config_write` 的 `ConfigVersion` |
| `agentkit/build/configtools.go:327` | ★ `config_write` 的 Note —— **说反了的那句** |
| `agentkit/build/configtools.go:385` | `config_get`（无 path）的 `effective` —— 运行中 |
| `agentkit/build/configtools.go:459` | `missingAgentError()` |
| `agentkit/build/configtools.go:466` | ★ 那句「all config_* tools read the snapshot」—— **说反了的那句** |
| `agentkit/build/tools.go:113` | `reservedToolNames` |
| `agentkit/build/tools.go:178` | 保留名的检查 —— provider 不能占用 |
| `agentkit/build/execute.go:40` | `newExecuteTool()` |

### 文件后端（`agentkit/fsx`）

| 位置 | 是什么 |
|---|---|
| `agentkit/fsx/fsx.go:229` | `resolveWrite()` —— 写入边界 |
| `agentkit/fsx/fsx.go:254` | `inside()` |
| `agentkit/fsx/fsx.go:728` | `Write()` |
| `agentkit/fsx/fsx.go:751` | `Edit()` |
| `agentkit/fsx/fsx.go:773` | ★ OldString 找不到就报错 —— 文件工具的冲突语义 |

### 运行期与组合根

| 位置 | 是什么 |
|---|---|
| `agentkit/run/loop.go:132` | `makePrepareAgent()` —— 每轮（也是每次恢复）取 agent |
| `agentkit/run/loop.go:347` | `makeGenResume()` |
| `agentkit/run/runtime.go:522` | 「a resume is a NEW run」 |
| `main.go:680` | `agentConfig` |
| `main.go:707` | `agentConfig.set()` —— `gen++` |
| `main.go:727` | `agentConfig.bump()` |

### 框架（上游，不在本仓库）

| 位置 | 是什么 |
|---|---|
| eino `adk/prebuilt/deep/deep.go:67` | `Backend` 非 nil 就注册整套文件工具 |
| eino `adk/prebuilt/deep/deep.go:219` | 挂 filesystem middleware 的那一行 |
| eino `adk/filesystem/backend.go:153` | `WriteRequest` —— 只有 `FilePath` + `Content` |
| eino `adk/filesystem/backend.go:162` | `EditRequest` —— `OldString` / `NewString`，**没有 revision** |

---

## 附录 B：与其它文档的关系

- `docs/config-design.md` —— **讲的是现在**。§8 是热更新的现状，§6 是配置表面。
  ★ 本文的 §4.5 之后要改掉的就是 §8.5 那条「换代即换树」的接线；
  两篇不打架：一篇讲现在，一篇讲将来。
- `docs/tool-surface-design.md` —— 六个 `config_*` 工具的现状。本文 §2 改的是它们的
  **答案形状**与**措辞**，不新增工具（除非 §7.2 第 1 条选了后者）。
- `docs/fs-backend-design.md` —— fsx 的边界与分叉。★ 本文 §3.2 给 fsx 加的是一个
  **它自己定义的窄接口**，不引入任何 openheron 依赖 —— 那份文档的「fsx 不依赖
  openheron」这条不变。
- `docs/run-lifecycle-design.md` —— run 是一行持久事实、有租约与 fence。
  ★ 本文 §4.4 要在 run 的行上多记一个**树指纹**。
