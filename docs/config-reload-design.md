# 配置重载与配置写入路径 —— 改造方案

> **状态：提案，未实施。**
>
> ★ 本文写的是**要改成什么样**，不是**现在是什么样**。现在的实现在
> `docs/config-design.md` §8（热更新）与 §6（配置表面）里；本文的每一条结论都从那里出发。
> 文末 §5 给出实施顺序与验收用例，§6 是需要 owner 拍板的问题。
>
> ★ **触发它的两件事都发生在真实使用里**，不是设想：
>
> 1. 一次 `config_write` 落在 `agents/main.md` 或 `agents/skill-expert.md` 上，
>    **子代理的运行被打断**：改动落盘了，报告回不来。换成 `edit_file` 就正常，
>    但那条路径没有修订冲突检查。
> 2. provider 抖动时，**子代理的最终报告是空的**，而空报告分不清「没改」和
>    「改完了但报告丢了」—— 于是只能回磁盘核对，再决定要不要重发。
>
> 第一件是配置系统自己的问题，第二件是它的下游症状。本文主要解决第一件。

---

## 1. 现在的样子：四个事实

★ 这一节只写事实，每一条都指到代码。后面所有设计都是从这四条推出来的。

### 1.1 重载是**全局换代**，不是按需重建

- `agentkit/spec/reload.go:204` `Watch()` 轮询各热分区的指纹；变了就
  `agentkit/spec/reload.go:246` 调 `Reload()`。
- `Reload()` 走 `onChange(cfg)` → `main.go:707` `agentConfig.set()` → **`gen++`**。
- `agentkit/build/snapshot.go:144` `snapshotFor(cfg, gen)`：代际变大就**重建整棵快照**
  （规格 + 每个 agent 的档位表）。
- `agentkit/build/pool.go:31` `GetOrBuild(ctx, name, gen, fn)`：池的键是 **(名字, 代际)**。
  代际一变，**每个** agent 都重建。
- ★ 而 `agentkit/build/assemble.go:100` `cfg.SubAgents = children` ——
  重建一个 agent 会把它的子树一起重建。

⇒ **改一个 `skills/<name>/SKILL.md`，整棵 agent 树在下一轮全部重建。**

### 1.2 一次运行**不钉住自己的 agent**

- `agentkit/run/loop.go:132` `makePrepareAgent` → `agentkit/build/builder.go:181`
  `b.mgr.Current()` → 取**当前**的配置与代际。
- 恢复审批走的是同一条路：`agentkit/run/runtime.go:522` 明写「a resume is a NEW run」，
  新 run 再走一次 `PrepareAgent`（`agentkit/run/loop.go:347` `makeGenResume` 之后）。
- ⇒ **一轮被审批打断再回来，树上已经是新代际**：被打断的那个 agent 的定义、
  档位表、甚至它自己的文件，都可能已经被换掉了。

★ 这就是「改动落盘了但报告回不来」的位置：报告是**那个 agent** 写的，
而恢复的时候它已经不是同一个 agent 了。

### 1.3 写配置有**两条路**，只有一条带检查

| 路径 | 落到哪 | 检查 |
|---|---|---|
| `config_write` | `agentkit/spec/write.go:484` `Service.Write()` | 修订检查、试装载、密钥扫描、「不许把能跑的 agent 弄丢」闸门、原子写 |
| `edit_file` / `write_file` | `agentkit/fsx` 直接写文件 | **无修订检查、无闸门** |

两条路落在**同一个文件**上。于是同一个改动有两种语义：一种带冲突检查，一种不带；
一种被闸门守着，一种把 `agents/main.md` 写坏了也只能等下次装载才发现。

### 1.4 现在的重载是**内容哈希**驱动的——这一点已经对了

- `agentkit/spec/reload.go:36` `HotDirFingerprints()`：每个热分区一个指纹，
  指纹里含**每个文件的内容哈希**（`agentkit/spec/reload.go:74` `dirEntryLines`）。
- ★ 也就是说：**真相已经在磁盘上**，重载不是靠「谁通知了我」，而是靠「重算一遍哈希」。
  这条是下面 §2 的地基：标记只能是**优化**，不能是真相。

---

## 2. 方案 A：重载改成「标记 + 下一轮按需重建」

### 2.1 提案一句话

> **写只标记；重建按依赖发生；一轮钉住自己开始时的那棵树。**

### 2.2 标记：写路径**不再**触发重建

- 写路径（`config_write` 与文件工具）做完闸门、落盘之后，**只做一件事**：
  把「哪些文件变了」记进一个**脏集合**。
- ★ **脏集合是缓存，不是真相。** 真相是磁盘上的内容哈希
  （§1.4）。所以脏集合丢了、重启了、清空了，**都不影响正确性** ——
  下一轮重算哈希时照样算得出变化。★ 这是「标记」这个词唯一安全的用法：
  它省掉一次全量扫描，不承担正确性。
- 脏集合放哪：**进程内**（`agentkit/build` 的 builder 或 `agentkit/spec` 的 service）。
  不写进 `state/`：它不是需要跨重启存活的事实。

### 2.3 重建的粒度：从**全局代际**换成**每个 agent 自己的依赖指纹**

★ 代际是**全局**的，所以任何改动都会让所有 agent 失效。要按需重建，就得把
「什么变了」换成**「谁变了」**——给每个 agent 一个**自己的**指纹。

**指纹的输入清单**（一个 agent 的指纹由这些东西决定）：

| 输入 | 为什么算进去 |
|---|---|
| `agents/<id>.md` 自己的文件 | 它的正文与 frontmatter |
| 它 `model:` 指向的那个 `llm/<provider>.yaml` | 模型、档位、超时 |
| 它依赖的 `tools/*.yaml` | 档位表（`allows:` 决定它要不要点头） |
| 它挂载的 `mcps/<server>.yaml` | 挂载即授予全部工具 |
| 它 `skills:` 列出的那些 `skills/<name>/` | 技能正文 |
| **它子 agent 的指纹**（递归） | ★ 父的 `task` 工具描述里嵌着子 agent 的名字与描述 —— 改了子 agent 的描述，父的工具描述就变了 |

⇒ **池的键从 (名字, 代际) 变成 (名字, 指纹)**（`agentkit/build/pool.go:31`）。
`GetOrBuild` 拿到的 `gen` 参数换成该 agent 自己的指纹：指纹没变就复用，
变了才重建。

★ **这一条顺带解决了「改一个 skill 重建整棵树」**：技能只进它自己那几个
agent 的指纹，别人的指纹不动，池里那份 entry 继续用。

### 2.4 ★ 一轮钉住自己的 agent —— 这是修「打断」的那一条

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
- ★ 这条与 §2.3 是同一件事的两面：指纹既是「谁变了」的答案，
  也是「我是谁」的答案。有了它，恢复不必猜「当前是第几代」。

### 2.5 MCP 的工具清单变化走同一条路

- 现在：MCP server 的清单变了 → `main.go:727` `bump()` → **全局** `gen++`
  （`agentkit/build/builder.go:107` `SetGenerationBump`）。
- 提案：清单变了 → **受影响 agent 的指纹变了**（挂了它的那些）→ 池按 §2.3 重建它们。
  ★ 不再需要 `bump()` 这条全局通道；**「代际」这个概念可以整体退休**。

### 2.6 提案明确**不做**的事

- ★ **不做「写完立刻重建」**：那正是现在的行为，也是打断的来源。
  写与重建解耦，是这一节全部内容的要害。
- ★ **不做「脏集合决定正确性」**：脏集合只决定「要不要重算」。
  重算永远以磁盘哈希为准（§1.4）。
- ★ **不改 `server/` 的非热重载**：它不在热分区里（`agentkit/spec/config.go:71`
  `Editable: false, hot: false`），本来就只影响进程自己的参数。

---

## 3. 方案 B：配置写入统一成「持久化文件的增改删查」

### 3.1 提案一句话

> **配置根里的文件，模型只有一条路能写；而这一条路自带修订检查与闸门。**

★ 「增改删查」指的是**对持久化文件做 CRUD**，不是「对内存里的树做 CRUD」。
现在 `config_write` 已经基本是了（写文件 + 校验 + 原子落盘），
**缺的是「删」和「别的工具写同一批文件时的同一套检查」**。

### 3.2 增删改查四个动作，与现有能力的对应

| 动作 | 现在 | 提案 |
|---|---|---|
| 查 | `agentkit/spec/write.go:382` `ReadFile()` —— `config_get` 已在用 | 不变 |
| 增 / 改 | `agentkit/spec/write.go:484` `Write()` —— `config_write` 已在用 | 不变，但**副作用收窄**（见 §3.4） |
| 删 | `agentkit/spec/write.go:762` `Delete()` —— **存在，但没有任何工具暴露** | ★ 待决：要不要给模型一个删除动作（§6 问题 3） |
| 校验 | `svc.Check()` / `svc.Diagnostics()` —— `config_validate` 已在用 | 不变 |

### 3.3 ★ 文件工具写配置根 ⇒ **转交** `spec.Service`，而不是各写各的

- 现在 `edit_file` 走 `agentkit/fsx`，直接写文件，**没有修订检查、没有闸门**
  （§1.3）。用户已经吃过这个亏：「换成 `edit_file` 后正常，但这条路径没有修订冲突检查」。
- **提案**：文件工具在解析出目标路径**落在配置根内**时，
  **不直接写**，而是：
  1. 读当前内容（`svc.ReadFile`）⇒ 拿到**当前 revision**；
  2. 在内存里应用补丁（`edit_file` 的「把 old 换成 new」语义、
     `write_file` 的整篇替换语义），得到**新内容**；
  3. 交给 `svc.Write(rel, newContent, baseRevision)` ——
     **修订检查、试装载、密钥扫描、agent 丢失闸门、原子写，全部照旧**。
- ⇒ 模型仍然可以「 surgical 地改一行」，但**改配置和改普通文件走同一套检查**。
  ★ 而 `baseRevision` 是**此刻**读到的 revision：模型拿着旧内容做的补丁，
  如果这期间有人改过同一个文件，`svc.Write` 会拒绝并报冲突 ——
  这正是「修订冲突检查」要的东西。

### 3.4 ★ 写的**副作用**收窄：落盘不再意味着「整棵树要换」

- 现在：写 → 落盘 → watcher 发现 → `gen++` → **下一轮整棵树重建**。
- 提案：写 → 落盘 → **只标记**（§2.2）→ 下一轮**按依赖**重建（§2.3）。
- ★ 于是「写配置」这件事的后果变得可预测：**它改的是磁盘；
  它什么时候影响一个 agent，由那个 agent 的依赖指纹决定。**
  这也让 §3.3 的「转交」变得安全：转交之后写配置**不会**顺带换掉正在跑的 agent。

### 3.5 提案保留的闸门（一个都不能丢）

`svc.Write` 现在做的检查，提案之后**一件不少**：

1. **修订检查** —— 基线 revision 必须与磁盘一致；
2. **试装载** —— 改完的树必须还能装载（否则不落盘）；
3. **密钥扫描** —— 不许把明文密钥写进配置；
4. **agent 丢失闸门** —— 不许把一个能跑的 agent 从树上弄掉；
5. **原子写** —— 不留半个文件。

★ 转交（§3.3）之所以可行，正是因为这五道闸门**在服务层**，
不在工具层 —— 工具换了路径，闸门不用重写。

---

## 4. 两个方案合起来：一次改动的时序

★ 场景：子代理 `skill-expert` 用 `config_write` 改 `agents/skill-expert.md`。

**现在**

```
子代理调用 config_write
  → 闸门 → 落盘
  → watcher 200ms 后发现 → gen++ → 快照重建
  → 【审批打断】轮次结束，报告还没写
用户批准
  → 新 run → PrepareAgent → 取当前代际 → 整棵树重建
  → 子代理是【另一个 agent】了（它自己的文件刚被改过）
  → 报告回不来 / 回的是另一个 agent 的话
```

**提案之后**

```
子代理调用 config_write
  → 闸门（修订/试装载/密钥/闸门/原子写）→ 落盘
  → 只标记「agents/skill-expert.md 变了」
  → 【审批打断】轮次结束，报告还没写
用户批准
  → 新 run → PrepareAgent → 要【指纹 = 本轮开始时那棵】的 agent
  → 池里那棵还在（多留一代）→ 拿到【同一个 agent】
  → 子代理继续跑 → 报告回来了
下一轮（真正的新 run）
  → 重算指纹 → skill-expert 的指纹变了 → 只重建它和它的父
  → 新配置生效
```

★ 差别只有一句话：**恢复的那一轮不再换树**。

---

## 5. 实施顺序与验收

### 5.1 分期

| 期 | 做什么 | 风险 |
|---|---|---|
| **P1** | §3.3：文件工具写配置根 ⇒ 转交 `svc.Write`（**不含**重载改造） | 小：只改写路径，不动重建 |
| **P2** | §2.2 + §2.3：标记 + 每 agent 依赖指纹，池的键换成指纹 | 中：重建粒度的核心 |
| **P3** | §2.4：run 钉住自己的树（多留一代 + 记住指纹） | 中：触及恢复路径 |
| **P4** | §2.5：MCP 清单变化并入指纹，全局 `bump()` 退休 | 大：代际这个概念退场 |
| **P5** | §3.2：删除动作（待决） | 小 |

★ **P1 可以单独上**：它解决「`edit_file` 没有修订冲突检查」这一条，
不依赖后面任何一期。而 P2 之前，**写配置仍然会整棵树重建** ——
所以 P1 单独上线时，「打断」还在。

### 5.2 验收用例（每一条都要有测试）

- 写 `agents/skill-expert.md` ⇒ **skill-expert 与它的父**被重建，
  **别的 agent 不重建**（池的 miss/hit 计数可断言）。
- 写 `skills/<name>/SKILL.md` ⇒ 只有 `skills:` 里点了它的 agent 被重建。
- 写 `tools/http.yaml` ⇒ 档位表受影响的 agent 被重建。
- ★ **恢复的那一轮**：审批打断 → 落盘 → 批准 ⇒ 恢复拿到**同一个 agent 实例**
  （断言指针相同或指纹相同），报告回来。
- ★ **下一轮**才看到新配置（断言第二轮的 agent 指纹变了）。
- `edit_file` 改 `agents/main.md` ⇒ 走 `svc.Write` ⇒ **基线 revision 不符时报冲突**。
- `edit_file` 改一个**普通文件** ⇒ 不走服务层，行为不变。
- `config_write` 写坏文件 ⇒ 闸门仍然拦下（树装不上 ⇒ 不落盘）。
- 重启进程 ⇒ 脏集合为空 ⇒ 第一轮重算哈希 ⇒ 仍然正确地重建。

---

## 6. 需要 owner 拍板的问题

★ 这一节的问题**会改变实现**，所以列出来而不是替 owner 决定。

1. **文件工具写配置根：转交还是拒绝？**
   提案是**转交**（§3.3）—— 保留模型的选择自由，同时拿到检查。
   另一种是**拒绝**（配置根对文件工具只读，写只能走 `config_write`）——
   语义更干净，但模型想「 surgical 改一行」时只能自己拼整篇内容。
2. **「下一轮生效」的边界**：恢复审批的那一轮算不算「下一轮」？
   提案说**不算**（§2.4）—— 它继续用旧树。若 owner 认为恢复也该用新树，
   那么「打断」的问题只能靠别的办法解（例如把审批从子代理里挪出去）。
3. **删除动作要不要给模型？** `svc.Delete` 已经在树里（§3.2），
   但没有工具暴露它。给了模型 `config_delete`，配置树就多一个能删东西的手。
4. **`server/` 要不要一起进这套机制？** 它现在是非热分区（只影响进程自身）。
   若进了指纹体系，`server/` 的改动也能在下一轮生效，而不必重启。
5. **旧 entry 留多久？** §2.4 说「没有 run 引用它才回收」。
   需要一个明确的口径（例如：留 N 代，或留到进程退出）—— 否则池会慢慢长。

---

## 附录 A：本文引用的代码位置

| 位置 | 是什么 |
|---|---|
| `agentkit/spec/reload.go:36` | `HotDirFingerprints()` —— 每个热分区一个指纹 |
| `agentkit/spec/reload.go:74` | `dirEntryLines()` —— 指纹里含每个文件的内容哈希 |
| `agentkit/spec/reload.go:204` | `Watch()` —— 轮询 → 重载 |
| `agentkit/spec/config.go:70` | `server/` 分区：不可编辑、非热 |
| `agentkit/spec/write.go:382` | `ReadFile()` —— 查 |
| `agentkit/spec/write.go:484` | `Write()` —— 增/改 + 五道闸门 |
| `agentkit/spec/write.go:762` | `Delete()` —— 删（无工具暴露） |
| `agentkit/build/pool.go:31` | `GetOrBuild()` —— 池的键 |
| `agentkit/build/pool.go:58` | `p.ents[name] = e` —— 换代即丢旧 entry |
| `agentkit/build/builder.go:107` | `SetGenerationBump()` —— 全局代际的推进钩子 |
| `agentkit/build/builder.go:181` | `b.mgr.Current()` —— 取当前配置与代际 |
| `agentkit/build/assemble.go:100` | `cfg.SubAgents = children` —— 重建带子树 |
| `agentkit/build/snapshot.go:144` | `snapshotFor()` —— 按代际缓存快照 |
| `agentkit/build/configtools.go:296` | `config_write` 调 `svc.Write()` |
| `agentkit/run/loop.go:132` | `makePrepareAgent()` —— 每轮（也是每次恢复）取 agent |
| `agentkit/run/runtime.go:522` | 「a resume is a NEW run」 |
| `main.go:707` | `agentConfig.set()` —— `gen++` |
