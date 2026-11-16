# openheron `/v2` 接口设计

> 状态：**已实施**（本文描述的就是代码面）。★ 未落地的提案集中在 §14。
>
> ★ **本文是本仓库的文档。** 上游 `heron` 仓库里那份 `api-v2-design.md` 写的是
> 老 `heron` 的 `/v2`（`heronrt/`、`agtransport/`、`a2ui/`、`static/index.html`）——
> 那套代码在本仓库**不存在**，行号一条都对不上。★ 把它抄进来是为了留下「`/v2`
> 是什么、为什么这么定」的规格与决策记录，**正文按本仓库的代码重写**。
>
> 关联文档：[runtime-design.md](./runtime-design.md)（运行时）·
> [config-design.md](./config-design.md)（配置）· [projects-design.md](./projects-design.md)（项目）·
> [secrets-design.md](./secrets-design.md)（密钥）· [tools-design.md](./tools-design.md)（工具与集成）·
> [pitfalls.md](./pitfalls.md)（坑册）

---

## 0. 一句话

**`/v2` 是 openheron 唯一的接口命名空间** —— 会话、运行、审批、文件、项目、配置、
密钥，全部在这里；前端只消费它，服务端是历史的唯一权威。

★ 与上游最大的不同：**`/v2` 里长出了会话的「公告通道」**（§7）——
它承载那些**不属于任何一轮**的事实：审批在等、审批被回答、新一轮开始。
上游那份文档里没有这条通道，因为上游没有「审批比它那一轮活得久」这回事。

---

## 1. 范围与三条硬约束

| 做 | 不做 |
|---|---|
| 定义 `/v2/*` 的 41 条路由（§2） | 不引入认证 / CORS / 限流（openheron 是本机单用户工具） |
| 后端产出 AG-UI 事件 | 不实现 A2UI（Go 侧零生态） |
| 暴露运行状态、审批详情、策略档位、用量 | 不让前端成为历史的事实源 |
| 让前端能水合历史 | 不改 `agentkit/run` 的审批 fail-closed 语义 |

**贯穿全文的三条硬约束**

1. **服务端权威历史** —— `RunAgentInput.messages` 仅作协议兼容，服务端只取末条
   user 消息作为 query，其余忽略。历史唯一事实源是会话的 transcript。
2. **显式生命周期** —— 会话只由 `POST /v2/sessions` 建；`POST /v2/run` 的
   `threadId` 必须已存在，缺失即 404。
3. **展示即前端责任** —— 角色配色、工具卡片形态、内容截断、分组折叠，全部由前端
   决定。后端只发语义化事件。

★ **第四条，是本仓库后来加的**：

4. **事实的归属决定它的通道** —— 一个事实属于一轮，就走那一轮的流（`/v2/run` 的
   SSE）；一个事实属于**会话**，就走会话的公告通道（§7）。★ 审批属于会话 ——
   抛出它的那一轮在抛出它的那一刻就结束了，而审批还在等一个答案。

---

## 2. 路由表

★ 全部注册在 `api/v2_files.go` 的 `registerV2Routes`（`api/v2_files.go:40`）。
★ 注册顺序是「**全部 `/v2/*` → SPA `NoRoute` 兜底 → `StaticFS("/")` 兜底**」——
一条放错位置的路由会静默变成 404，读起来跟「端点没实现」一模一样。
★ **这个顺序必须被测试锁住**（`api/v2_test.go:37` 的路由优先级用例）—— 这是典型的
「重构时不看顺序就静默坏掉」的坑。

| # | 方法 | 路径 | 说的是什么 |
|---|---|---|---|
| 1 | `POST` | `/v2/sessions` | 建会话（唯一入口） |
| 2 | `GET` | `/v2/sessions` | 列会话 |
| 3 | `GET` | `/v2/sessions/:id` | 一个会话的详情 |
| 4 | `PATCH` | `/v2/sessions/:id` | 改名 |
| 5 | `PATCH` | `/v2/sessions/:id/agent` | 换根 agent |
| 6 | `DELETE` | `/v2/sessions/:id` | 删会话 |
| 7 | `GET` | `/v2/sessions/:id/messages` | 历史回放 + 待批 + 用量 |
| 8 | `POST` | `/v2/sessions/:id/abort` | 停止 |
| 9 | `POST` | `/v2/sessions/:id/compact` | 压缩上下文 |
| 10 | `GET` | `/v2/sessions/:id/runs` | **账本扫描**：这个会话跑过什么 |
| 11 | `GET` | `/v2/sessions/:id/runs/:runId` | 账本里的一行 |
| 12 | `GET` | `/v2/sessions/:id/events` | ★ **会话的公告通道**（§7） |
| 13 | `POST` | `/v2/sessions/:id/files` | 上传（多文件） |
| 14 | `GET` | `/v2/sessions/:id/files` | 列工作区 |
| 15 | `GET` | `/v2/sessions/:id/files/raw` | 一个文件的字节（预览用） |
| 16 | `DELETE` | `/v2/sessions/:id/files` | 删一个文件 |
| 17 | `GET` | `/v2/project/files` | 项目文件（composer 的 `@` 提示用） |
| 18 | `GET` | `/v2/commands` | 斜杠命令表 |
| 19 | `GET` | `/v2/projects` | 项目表 |
| 20 | `POST` | `/v2/projects` | 建项目 |
| 21 | `PATCH` | `/v2/projects/:id` | 改项目（目录 / 名字） |
| 22 | `DELETE` | `/v2/projects/:id` | 删项目 |
| 23 | `POST` | `/v2/fs/pick-directory` | 打开**操作系统**的目录选择器 |
| 24 | `POST` | `/v2/run` | 跑一轮（提问 / 恢复审批） |
| 25 | `GET` | `/v2/skills` | 技能清单 |
| 26 | `GET` | `/v2/agents` | agent 清单 |
| 27 | `GET` | `/v2/agents/:agentId` | 一个 agent + 它的生效工具表 |
| 28 | `GET` | `/v2/mcps` | MCP 服务器与它们的工具 |
| 29 | `GET` | `/v2/config` | 配置视图 |
| 30 | `GET` | `/v2/health` | 健康 |
| 31 | `GET` | `/v2/runtime` | 运行时与一致性巡检 |
| 32 | `GET` | `/v2/settings` | 配置树的清单 |
| 33 | `GET` | `/v2/settings/item` | 读一个配置文件 |
| 34 | `POST` | `/v2/settings/item` | 写一个配置文件 |
| 35 | `PUT` | `/v2/settings/item` | 同上（`PUT` 是同一支） |
| 36 | `DELETE` | `/v2/settings/item` | 删一个配置文件 |
| 37 | `POST` | `/v2/settings/validate` | 校验一份配置草稿 |
| 38 | `GET` | `/v2/secrets` | 密钥清单（**永不含值**） |
| 39 | `PUT` | `/v2/secrets/:group/:name` | 写一把钥匙 |
| 40 | `DELETE` | `/v2/secrets/:group/:name` | 删一把钥匙 |
| 41 | `DELETE` | `/v2/secrets/:group` | 删一个组 |

★ **扫描的路由只读**（`api/v2_files.go:57`）—— 一条读路由若会写账本，扫描就分不清
「是什么」与「刚被修好了」。

★ **主机守卫**在路由之外：`api/guard.go` 的 `Guard.Reject`（`api/guard.go:61`）按
`Host` 与 `Origin` 拒绝跨站请求，写操作尤其（`api/guard.go:122` 的 `mutating`）。

---

## 3. 会话

### 3.1 `POST /v2/sessions`

请求体可选：

```json
{ "agentId": "task", "projectId": "11111111-1111-4111-8111-111111111111" }
```

- `agentId` 可选：**建会话时定根**，省掉「先建、再 `PATCH` 改根」的一次往返。
- `projectId` 可选：会话归属哪个项目。★ 归属**不可改** —— 见
  [projects-design.md](./projects-design.md) §2.3。
- 名字先校验再建会话：写错时**不留下一个空会话**。

**201 Created**

```json
{
  "threadId": "3f2a1c8e-5b44-4a2b-9e83-b343567c46c6",
  "title": "New Session",
  "createdAt": "2026-10-11T09:12:33.481Z",
  "updatedAt": "2026-10-11T09:12:33.481Z",
  "messageCount": 0,
  "state": "idle",
  "hasPendingApproval": false,
  "agentId": "main",
  "projectId": ""
}
```

★ `agentId` 与 `projectId` **总是出现**：空串就是答案「没选」，而缺字段说的是
「这个版本不知道有这回事」—— 另一件事。

★ **默认 agent 是 main，且不是配置项**：「谁是默认」不是配置项，只是「新会话没指定
时用哪个」的约定。

★ **会话创建时机的三版取舍**（这一节的结论改过两次，每一版都有它的问题）：

| 版本 | 规则 | 问题 |
|---|---|---|
| **懒创建**（最早的 v2） | `threadId` 可选，缺了就由 `/v2/run` 此刻创建 | 「谁决定建会话」没有归属：客户端以为服务端会建、服务端以为客户端会传 id |
| **预创建 + 本地槽位** | `POST /v2/sessions` 唯一入口，但前端只持有**本地槽位 id** | ① 两次往返；② **双 id** —— 助手 UI 在发消息之前就要一个 thread id 当列表 key，那个 id 只能是本地占位，于是每个会话前后两个 id，需要一整套注册表去收敛，收敛失败时 `GET /v2/sessions/{slot}/messages` 打 404，历史整个空掉 |
| **现在的** | `POST /v2/sessions` 唯一入口，**前端决定建不建** | 第一条消息两次往返（建 + 跑），之后都是一次 |

★ 为什么最后选「前端决定」：服务端只能从「有没有 threadId」推断，而那是**结果**不是
**决定**。「建不建」得有归属 —— 归属在知道当前有没有会话的那一端，也就是客户端。
★ 双 id 的问题在现在这版**不存在**：前端建完会话就拿到了**真 id**，本地占位与真实 id
是同一个东西，没有「收敛」这一步。

★ **试过并否决的中间方案**：`CreateWithID`（前端生成 uuid、服务端首次写入时落盘）
—— ★ 前端必须猜 `validateID` 的规则，格式漂移的表现是 404，而在懒创建语义下 404
会被理解成「会话丢了」，排查方向完全错位；以及「读写都自动创建」—— ★ 读写不对称，
读路径 404 / 写路径创建这条边界很容易在后续改动中被无意抹平，一旦抹平，**拼错的
URL 会静默造出会话**。现在只有 `POST /v2/sessions` 一个创建入口，读路径一律 404。

### 3.2 `GET /v2/sessions`

**200 OK** `{"items": [ … ]}`，每项的形状与 §3.1 的响应体相同。

★ 排序按 `updatedAt` 降序；标题来自**缓存字段**，不是每次重开全部会话文件算出来的。

### 3.3 `GET /v2/sessions/{threadId}`

**200 OK**：§3.1 的形状，另加：

| 字段 | 说的是什么 |
|---|---|
| `runId` | 当前拥有这个会话的那一轮（从**账本**读，不是从运行时读） |
| `startedAt` / `lastError` | 这一轮什么时候开始的 / 上一次怎么失败的 |
| `pendingApprovals` | 待批的卡片数据（§6.2） |
| `decisions` | 已经做过的决定 |

**404** → `{"error":"session not found","code":"thread_not_found"}`

**`state` 的推导规则**（`api/v2_sessions.go:559` 的 `stateOf`）

```
待批非空                → "waiting_approval"
账本里有活着的轮次       → "running"
否则                    → "idle"
```

### 3.4 `PATCH /v2/sessions/{threadId}`

请求 `{"title": "新标题"}`，长度 1–120。**200 OK** 返回更新后的 meta。

### 3.5 `PATCH /v2/sessions/{threadId}/agent`

切换会话的**根 agent**。只影响之后的轮次：历史不改写，正在跑的轮次不会换归属。

**请求** `{"agentId": "task"}` → **200 OK** 返回更新后的 meta。

| 场景 | 状态码 | `code` |
|---|---|---|
| agent 不存在 / 没声明 | 400 | `agent_not_found` |
| 运行中 / 待审批 | 409 | `session_busy` |
| 会话不存在 | 404 | `thread_not_found` |

★ **不限制层级**：每个**已声明**的 agent 都能当会话的根，嵌套多深都行。

★ **运行中直接 409 而不是排队**：切换发生在轮次中途会留下「前半段按 main 跑、
后半段按 task 归档」的记录，而两边都自洽 —— 这种历史最难查。

### 3.6 `DELETE /v2/sessions/{threadId}`

**200 OK** `{"threadId":"…","deleted":true}`；**404** 会话不存在。

★ 旧接口对未知 id 也返成功，v2 收紧到 404。

---

## 4. 运行

### 4.1 `POST /v2/run`

一个端点，两种情形（靠 `resume` 在不在来分支）。

**情形 ① · 新提问**

```json
{ "threadId": "3f2a1c8e-…", "query": "解释 a2ui/streamer.go" }
```

`threadId` **必填**。缺 → **400** `invalid_body`；给了但没有这个会话 → **404**
`thread_not_found`。

★ **「建不建会话」的决定权在客户端**：服务端只能从「有没有 threadId」推断，而那是
**结果**不是**决定**。⇒ 规则只有一条：**要跑，先有会话**。

**情形 ② · 审批恢复**

```json
{
  "threadId": "3f2a1c8e-…",
  "resume": [
    { "interruptId": "ic-7f3a", "payload": { "approved": true, "reason": "" } }
  ]
}
```

情形 ② **不允许省略 `threadId`** —— 对一个不存在的会话答复审批是调用方的 bug，
静默建个空会话会把它埋成「审批点了没反应」。

**`query` vs `messages`**

`messages`（AG-UI 的完整对话数组）保留但**不作为主入口**：服务端只读最后一条 user
文本（`api/v2_run.go:371` 的 `lastUserText`）。取值优先级：`query` → 回退到
`messages` 里的最后一条 user 文本。

**分支判定**：`len(resume) == 0` → `rt.Submit`；否则 → `rt.Resume`。

**校验表**

| 条件 | 状态码 | `code` |
|---|---|---|
| body 非法 JSON / 结构不符 | 400 | `invalid_body` |
| `resume` 存在但 `threadId` 省略 | 400 | `invalid_body` |
| `threadId` 给了但会话不存在 | 404 | `thread_not_found` |
| `query` 与 `messages` 都给不出非空文本 | 400 | `empty_message` |
| 有 `resume` 但无待批 | 409 | `no_pending_interrupt` |
| `resume` 里**任何一个** id 不在当前待批集合里 | 409 | `interrupt_mismatch` |
| 这个会话有活着的轮次，或有待批 | 409 | `session_busy`（**可重试**） |
| 审批的恢复点没了 | 409 | `no_checkpoint` |

★ **`interrupt_mismatch` 是新增防线**：`rt.Resume` 只按 `TakePendingApproval()` 取待批，
**完全不校验前端传来的 id** —— 客户端可以携带一个陈旧的或属于另一轮次的
interruptId，服务端照单全收。v2 要求先比对再放行。

★ 校验的是**每一个** id，比对的基准是**待批集合**而不是某一个 id。一轮里可以有多个
独立待批；整组里混进一个不在表里的 ⇒ **整个请求被拒**，不是「跳过那个、答其余的」
—— 那样客户端会以为它答过了。

★ **答复必须一次答完整组**：框架的契约是，不在 `Targets` 里的被中断组件会**重新
中断自己**，而重新中断拿到的是**新的 id**。逐个发的话，答完第一个，其余的 id 立刻
作废。

**`resume` 项里没有 `status`**

AG-UI 的 resume entry 带 `status`（`resolved` / `cancelled`），但那描述的是「这个
中断被处理掉了没有」，不表达「用户同不同意」。服务端读的是 `payload.approved`，
`status` 被忽略 —— 字段已从结构体里**删掉**而不是留着忽略：一个「到了却什么都不做」
的字段会教会每个后来者 `status` 能决定什么。

**响应**：`200 text/event-stream`，每帧 `data:{AG-UI 事件}\n\n`。

★ **冒号后没有空格**。SSE 规范允许（字段值只剥一个前导空格），但按 `data: ` 严格
匹配的解析器会**一条都收不到且不报错**。前端 `web/src/lib/sse.ts` 两种都认。

### 4.2 挂回去：`POST /v2/run` 带 `runId`

★ 这是**第三种情形**，上游那份文档里没有：客户端拿着一个轮次 id 回来，要挂回那一轮
的流。

```json
{ "threadId": "3f2a1c8e-…", "runId": "01J8X7K2M9" }
```

★ 判据是 `isAttachRequest`（`api/v2_run.go:224`）：带 `runId` 且不带 `query`/`resume`
就是挂回去。挂不上的答复是 **404 `run_not_live`**（`api/v2_types.go:58`）。

★ **`run_not_live` 是它自己的码**，因为「你要挂的那一轮没了」既不是「会话忙」也不是
「没有这一轮」：客户端对它的回答是「去读历史」，而这必须与「可以重试的忙」分开。

★ 挂回去**有窗口**：那一轮结束后、审批落盘前，运行时**还占着会话的槽位**
（`agentkit/run/bridge.go`），所以 attach 被拒是有意的 —— 客户端要等账本的屏障
（`docs/runtime-design.md` §1.2）。

---

## 5. 轮次自己的 SSE 契约

`POST /v2/run` 的响应体是这一轮的流。

| 事件 | 触发源 | 关键字段 |
|---|---|---|
| `RUN_STARTED` | 请求受理 | `threadId` `runId` |
| `REASONING_START` … `REASONING_END` | 模型思考 | `messageId` |
| `TEXT_MESSAGE_START` / `_CONTENT` / `_END` | 正文 | `messageId` `delta` |
| `TOOL_CALL_START` / `_ARGS` / `_END` | 工具调用 | `toolCallId` `toolCallName` |
| `TOOL_CALL_RESULT` | 工具返回 | `messageId` `toolCallId` `content` |
| `CUSTOM` | 模型重试 | `name:"model_retry"` `value:{message}` |
| `CUSTOM` | 上下文占用 | `name:"context_usage"` `value:{agent,used_tokens,…}` |
| `CUSTOM` | 压缩 | `name:"context_compacted"` `value:{agent,at}` |
| `RUN_FINISHED` | 正常结束 | `outcome.type:"success"` |
| `RUN_FINISHED` | 等待审批 | `outcome.type:"interrupt"` `outcome.interrupts[]` |
| `RUN_ERROR` | 运行失败 | `code` `message` |

★ **`RUN_FINISHED.usage[]` 未实现** —— SDK 的 `events.WithUsage` 在本仓库零调用；
用量只从 `/messages` 暴露（§6.3）。两者是不同的通道：`usage[]` 管厂商记账，
`context_usage` 管占用。

★ **`interrupts[]` 项**

```json
{ "id": "ic-7f3a", "reason": "tool_call",
  "message": "tool write_file requires approval",
  "metadata": {
    "toolName": "write_file",
    "arguments": { "file_path": "/tmp/x.txt" }
  },
  "subagentRunId": null }
```

★ **`metadata` 是审批卡的内容来源**：前端不能靠 `message` 反解工具名与参数 ——
它是给人看的兜底文案。`toolName` / `arguments` 直接来自 `*approval.ApprovalInfo`；
`arguments` 只在是合法 JSON 时出现。

### 5.1 ★ 实测踩出来的七条

★ 这七条是**真模型联调**撞出来的：每一条看起来都像「模型的问题」，实际是**契约的
问题**。★ 上游那份把它们记在 `heron` 的坐标上；搬过来时每一条都重新定位到了本仓库
现在的那一行。

1. **审批链路需要 `gob.Register`。** `ApprovalInfo` 挂在 eino interrupt 的 `Info`
   字段上（接口值），而 checkpoint 用 gob 序列化 —— ★ gob 对接口字段的具体类型必须在
   包初始化时 Register，否则编码失败。★ 失败发生在**停机提交 checkpoint 的那一刻**，
   现象是「模型说要调工具 → 直接 RUN_ERROR」，看着像模型出错，实际是编码器。
   本仓库：`approval/approval.go:18` 与 `:19`；守着的测试是
   `agentkit/build/gate_test.go:84` 的 `TestApprovalInfoSurvivesGob` 与
   `approval/approval_test.go:9` 的 `TestDecisionSurvivesGob`。

2. **interrupt 的 message 不能用 `%v`。** `fmt.Sprintf("%v", ic.Info)` 会把
   `*ApprovalInfo` 打平成 `&{write_file {"file_path": ...}}` —— 这段文本经事件流直达
   前端审批卡，等于把内部 Go 表示泄漏成界面文案。本仓库走 `describe(ic.Info)`
   （`agentkit/emit/emit.go:444`），守着的测试是
   `agentkit/emit/interrupt_test.go:14` 的 `TestTheInterruptDescribesTheApprovalInfo`。

3. **`state` 停在 `running`。** 轮次完成后 turn 句柄仍被持有，于是
   `GET /v2/sessions/{id}` 永远报 `running`。★ 根因是「loop 还活着」（在等下一条
   item）被当成了「有活动在进行」—— 这两件事不等价。修法是 turn 一旦上报就释放引用
   （`agentkit/run/bridge.go:73` 的 `markTurnDone`），★ **只清匹配的那个句柄**，免得
   误伤期间被替换掉的 turn。整套运行时设计在 `docs/runtime-design.md`。

4. **SSE 分帧是 `data:{`，不是 `data: `。** `data:{...}`（冒号后无空格）是合法的
   SSE，但任何按 `data: ` 严格匹配的解析器都会**静默地一条都收不到** —— 不报错、不
   抛异常，流只是空的。★ AG-UI 是跨语言契约，这个缝隙必须显式测。本仓库的解析器在
   `web/src/lib/sse.ts:43` 只吃掉**一个**可选空格，所以两种都收；断言在
   `web/scripts/interop-check.ts:34` —— ★ 它用的是**生产路径上那个**解析器，不是
   自己另写一个假装兼容（用第三方 SDK 验等于验错了对象）。

5. **审批 resume 是两层语义。** AG-UI 规范里 resume entry 的 `status` 只有
   `resolved` / `cancelled`，描述的是「中断被处理掉没有」，不表达「用户同不同意」；
   openheron 读的是 `payload.approved` —— ★ 这样拒绝理由才能跟着 payload 过去。
   ★ `status` **不在结构体上**，而不是「接收并忽略」：一个「到了却什么都不做」的字段
   会教会每个后来者 `status` 能决定什么。本仓库：`api/v2_run.go:108` 的 `resumeEntry`
   —— 它只有 `interruptId` 与 `payload`（`approved` + `reason`）。

6. **`REASONING_MESSAGE_START` 的 `role` 只有前端会校验。** 必须是字面量
   `"reasoning"`。★ Go 侧 `events.Validate()` 不查这个字段，契约测试全绿也照样漏；
   前端拒绝后整条事件丢弃，表现为「推理一出现，后面所有内容都没了」。★ 这类**只有
   单侧有的校验**是跨语言协议最危险的地方：Go 侧加一条断言只能保证「我们自己没写
   错」，保证不了「对方认不认」。本仓库：`transport/transport.go:398` 的
   `reasoningRole`。

7. **`TOOL_CALL_RESULT` 的 `messageId` 不是 callId。** 它是一次新生成的 `uuid`
   （`transport/transport.go:445`）。★ 前端按 `toolCallId` 回填就够（工具调用可能
   挂在别的 `parentMessageId` 上），但这个字段确实存在 —— 它是在把前端的 fixture 从
   `Record<string, unknown>[]` 改成 `AgEvent[]` 之后冒出来的。

### 5.2 ★ 传输层的两条

★ **sink 断了要停止渲染，但必须继续消费事件流** —— 否则归档的消息会缺，`Outcome`
不完整。★ 这条是上游从 `a2ui` 照搬来的，本仓库的传输层也守着它：写失败之后
`emit` 直接返回（`transport/transport.go:220`），而 `Render` 的循环**不退出** ——
事件继续被消费掉。

★ **契约测试用 SDK 自带的 `events.ValidateSequence`**，它强制校验
`RUN_STARTED` 先于 `RUN_FINISHED` / `RUN_ERROR`、`TEXT_MESSAGE_START` →
`CONTENT`… → `END` 配对、`TOOL_CALL_START` → `ARGS`… → `END` 配对、
`outcome.type == "interrupt"` 时至少一个 interrupt。★ 强度远高于自己手写断言。
本仓库：`transport/transport_test.go:93`。

---

## 6. 历史回放

### 6.1 `GET /v2/sessions/{threadId}/messages`

**200 OK**

```json
{
  "threadId": "3f2a1c8e-…",
  "state": "waiting_approval",
  "messages": [ … AG-UI 原生的 types.Message … ],
  "pendingApprovals": [ … ],
  "usage": { "promptTokens": 18422, "completionTokens": 931,
             "totalTokens": 19353, "reasoningTokens": 612,
             "cachedTokens": 8192,
             "byAgent": { "main": { … }, "coder": { … } } },
  "agent": "main"
}
```

★ `messages` 是 **AG-UI 原生 `types.Message`**，前端用**同一个事件归约器**水合流式
与回放两路数据 —— 一套状态机，两个数据源。

★ **`?agent=xxx` 按 agent 过滤消息**（`api/v2_sessions.go:326`）：

| 情形 | 响应 |
|---|---|
| 有该 agent 的消息 | `200`，`messages` 只含该 agent 的消息；`agent` 回显 `"coder"` |
| 没有该 agent 的消息 | `200`，`messages` 为空数组（**不是 404** —— 「过滤条件不认识」与「会话不存在」是两回事） |

★ **过滤只影响 `messages`，不影响 `usage` 与 `usage.byAgent`**：用量是会话级的
事实，而过滤是「看哪些消息」。★ 若两者一起收窄，客户端拿到的 `byAgent` 各桶之和就
对不上 `usage`。

★ **过滤时保留消息的原始下标** —— 消息 id 由下标派生（`stableID`），改了它，同一条
消息在「全部视图」与「按 agent 视图」里会有两个 id，前端的重连与去重就乱了。

### 6.2 `pendingApprovals` —— 待批的卡片数据

```json
{
  "interruptId": "ic-7f3a",
  "reason": "tool_call",
  "message": "write_file needs approval",
  "toolCallId": "call-9x2",
  "toolName": "write_file",
  "arguments": { "path": "config.yaml" },
  "requestedByAgent": "main",
  "subagentRunId": null,
  "createdAt": "2026-10-11T09:14:12.004Z"
}
```

★ **由服务端显式给出**，前端**不需要任何启发式**。它是**列表**：一轮里可以有多个
独立待批。

★ `reason` 恒为 `tool_call`，而这个字**只有一份**：`contract.PendingReasonToolCall`
（`contract/contract.go`）。★ 三处曾经各写各的（扫描的 DTO、传输层、emit 包），
现在共用一个常量 —— 一张卡在这里说是 `tool_call`、在那里说是别的，是一张没人读得
懂的卡。

### 6.3 `usage`

★ 各桶之和 **==** `usage.totalTokens`：分账与总量同源（都从消息上的归属标记读）。

★ 没有归属标记的消息（加标记之前的老会话）**不进任何桶** —— 归给「unknown」会让
分账看起来比实际更完整。

---

## 7. 会话的公告通道

### 7.1 `GET /v2/sessions/{threadId}/events`

★ 这是本文的**新增部分**，上游那份文档里没有。

它是一条 **SSE**，推的是**只属于会话**的事实。

★ **为什么要有它**：审批属于**会话**，不属于任何一轮。抛出它的那一轮在抛出它的那
一刻就结束了，而审批还在等一个答案。于是：

- 一个**挂着那张卡**的界面，必须在别人回答它时知道 —— 否则它给出一个服务端已经拒
  绝的答案；
- 一个**没有那张卡**的界面，必须在审批出现时知道 —— 否则它不知道该问什么。

★ **这两件事都不是那一轮的流能说的**：那一轮的流在审批抛出后就关了。

★ **事实是通知，不是真相。** 真相是扫描（`/v2/sessions/{id}/messages`）与账本
（`/v2/sessions/{id}/runs`）。★ 客户端**先订阅、后扫描**：扫描之前发生的事在扫描
里，扫描之后发生的事从这里来。

★ **通道没有终态帧。** 它在客户端离开时结束，而客户端把这读成「回去扫描」，不是
「这个会话结束了」。

### 7.2 三个事实

| 事实 | 帧名 | 什么时候发 | 载荷 |
|---|---|---|---|
| 一轮开始 | `run_started` | 轮次申领成功、loop 启动之后 | `run_id` `kind` |
| ★ **审批在等** | `approval_pending` | **审批落盘、账本终态行立起之后** | 见 §7.3 |
| 审批被回答 | `approval_resolved` | 审批被消费之后 | `interrupt_id` `approved` `reason` `run_id` |

★ 三条都是 **CUSTOM 帧**：AG-UI 没有「审批在等」这种词汇，而它的帧都是关于**一轮**
的 —— 这三条关于**会话**。

★ **顺序有意义**：`run_started` → `approval_pending` → （答了）→ `approval_resolved`
→ `run_started`（恢复的那一轮）。一个跟着一轮走的界面，必须先知道卡已经作废，再去
挂下一轮。

### 7.3 ★「审批在等」这条事实

```json
{
  "type": "CUSTOM",
  "name": "approval_pending",
  "value": {
    "interrupt_id": "ic-7f3a",
    "reason": "tool_call",
    "message": "write_file needs approval",
    "tool_call_id": "call-9x2",
    "tool_name": "write_file",
    "arguments": { "path": "config.yaml" },
    "requested_by_agent": "main",
    "subagent_run_id": null,
    "created_at": "2026-10-11T09:14:12.004Z",
    "run_id": "01J8X7K2M9"
  }
}
```

★ **它带的是卡片的内容，不只是「有一张卡」**：一个没有看着那一轮的界面没有自己的
流，工具名与参数对它来说**别处不存在** —— 而一张没有内容的卡是一张没法判断的卡。

★ **载荷是会话持久化的那份形状**（`contract.PendingInterrupt`），所以两个表面说的
是**同一张卡**。★ 但**拼写不同**：扫描用 REST 的驼峰（`interruptId`），通道用它自己
的蛇形（`interrupt_id`）—— 两个表面，两种拼法，客户端在**一处**翻译
（`web/src/lib/session-events.ts` 的 `readInterrupt`）。

★ **`arguments` 是工具调用自己的字节**（`json.RawMessage`），不是字符串：客户端不
必再解析一次。

#### ★ 什么时候发

**审批落盘、账本终态行立起之后。**

★ **落盘之后**：一个没有持久化的审批**答不了** —— 提前宣布它，就是给一个界面一张
点了就报错的卡。★ 落盘失败时**不宣布**（`agentkit/run/bridge.go` 的
`writeHeldApproval` 返回 nil）。

★ **屏障之后**：账本的行已经是终态（`waiting_approval`），于是事实与账本说的是同一
件事 —— 一个界面先显示卡、再去扫账本，不能看到「一边在跑、一边在等批准」。

★ 代码位置：`agentkit/run/bridge.go` 的 `loopExited` —— 写审批 → 立屏障 → 关发布方
→ **宣布** → 释放槽位。

### 7.4 客户端怎么用

★ 前端在 `web/src/lib/session-events.ts`：三个事实一个解析器、一个折叠函数。

- `approval_pending` → 把那张卡**并进** `convo.interrupts`（按 id 去重），状态改成
  `awaiting-approval`。★ **去重是必须的**：发起那一轮的那一边是从轮次自己的流里看到
  这张卡的，通道说的是**同一张**。
- `approval_resolved` → **摘掉**那张卡。
- `run_started` → 清掉旧卡（那一轮已经结束，它问的问题作废了），状态改成
  `running`，然后去挂那一轮。

★ **通道没有回放**：一个后来才订阅的界面拿不到过去的事 —— 过去是扫描的活。

★ **未知的事实不是错误**：通道是活的通知路径，服务端会往里加事实。一个**半懂**的
帧才是问题（没有 id 的审批指不到任何一张卡），所以它被丢掉。

---

## 8. 运行控制

### 8.1 `POST /v2/sessions/{threadId}/abort`

| 情形 | 响应 |
|---|---|
| 有运行中的轮次 | `200 {"threadId":"…","aborted":true,"runId":"01J8X…"}` |
| 会话存在但空闲 | `200 {"threadId":"…","aborted":false,"reason":"idle"}` |
| 会话不存在 | `404 {"error":"session not found","code":"thread_not_found"}` |

★ **这个端点是「停止」按钮的唯一真实作用点。** 只断开前端那条 SSE 连接**不等于**
中止：服务端那一轮会继续跑到底 —— 模型继续烧 token、工具继续执行副作用，而界面
已经显示 idle。那是「假停止」，比没有停止按钮更糟。

★ 中止的语义是**留 checkpoint**：中止的那一刻工具可能已经产生了副作用，删掉
checkpoint 就等于假装什么都没发生。

### 8.2 `POST /v2/sessions/{threadId}/compact`

手动压缩这个会话的上下文。

| 情形 | 状态码 | `code` |
|---|---|---|
| 轮次在跑 | 409 | `session_busy` |
| 会话不存在 | 404 | `thread_not_found` |

★ 压缩本身是**会话级**的：它改的是这个会话的历史，而历史归会话。

---

## 9. 工作区文件

### 9.1 `POST /v2/sessions/{tid}/files`

`multipart/form-data`，字段 `files[]`（**可多个**）。

**201 Created**

```json
{
  "files": [
    { "name": "spec.md", "path": "spec-9f2a.md", "size": 4821, "mime": "text/markdown" }
  ],
  "workspacePath": "/…/workspace"
}
```

| 条件 | 状态码 | `code` |
|---|---|---|
| 无文件 | 400 | `file_required` |
| 单文件超限（32 MiB） | 413 | `file_too_large` |
| 路径逃逸 | 400 | `invalid_file_name` |

★ **`path` 是相对会话 workspace 的名字**，不是磁盘上的绝对路径 —— 客户端要把它
**原样送回来**，所以它必须是服务端自己认得的名字。

★ **`name` 是用户认得的那个名字**，盘上那个是带哈希的：给模型看的是 workspace，
而哈希名字对它毫无意义（`api/v2_files.go:234` 的 `imaging.DisplayName`）。

★ 图片在**服务端**压缩，格式保持（PNG 还是 PNG，透明像素还在）；其它格式原样通过。

### 9.2 `GET /v2/sessions/{tid}/files`

```json
{ "workspacePath": "/…/workspace",
  "files": [ { "name": "spec.md", "path": "spec-9f2a.md", "size": 4821,
               "mime": "text/markdown", "modifiedAt": "2026-10-11T09:02:11Z" } ] }
```

### 9.3 `GET /v2/sessions/{tid}/files/raw`

一个文件的字节，给预览用（`api/v2_files.go:291`）。

★ **它从不解释内容**，只有四种回答：

- 图片：它自己的类型，inline；
- PDF：inline，给浏览器自己的阅读器；
- 文本：**`text/plain`**，inline —— **绝不是 `text/html`**；
- 其余：octet-stream，作为下载。

★ **文本那一条是要紧的**：一个叫 `.html` 或 `.svg` 的文件可以带脚本，而把它当文档
或当图片渲染的浏览器会在**这个源**上跑那个脚本。按文本给出就关上了这扇门。

★ **SVG 不算图片** —— 它能带脚本，而浏览器把它当文档渲染。

### 9.4 `DELETE /v2/sessions/{tid}/files`

`?path=` 相对 workspace 的名字。**200 OK** `{"deleted":true,"path":"…"}`。

★ 粒度是**文件**：一条消息的附件清单是**历史**（它说「这个问题是带着这个文件来的」），
而历史不因文件被删而重写。

### 9.5 `GET /v2/project/files`

供 composer 的 `@` 提示枚举**项目**文件（不是会话 workspace）。根目录是项目的目录。

查询参数：`q`（子串）、`limit`（默认 50）。遍历是 BFS，不排除任何目录；扫描 50,000
条、候选 2,000 条后停止并返回 `truncated: true`。符号链接不跟随。

---

## 10. 项目

★ 项目就是**目录**。归属只有一份真相：`state/projects.json`（见
[projects-design.md](./projects-design.md)）。

| 方法 | 路径 | 说的是什么 |
|---|---|---|
| `GET` | `/v2/projects` | 项目表 + 当前作用域 |
| `POST` | `/v2/projects` | 建项目（给一个目录） |
| `PATCH` | `/v2/projects/:id` | 改名字或改目录 |
| `DELETE` | `/v2/projects/:id` | 删项目 |

★ **作用域在答案里**（`projectScope`）：`groups` 告诉客户端要画几个分组，`current`
告诉它当前在哪一个。

★ 删项目的码是 `project_not_found`（`api/v2_projects.go:20`）—— 它是**自己的**码，
因为「项目没了」与「会话没了」是不同的错误，客户端要能分开。

★ **会话的归属不可改**：换项目 = 新建会话。

★ **`POST /v2/fs/pick-directory`**（`api/v2_dirpick.go:59`）：打开**操作系统**的目录
选择器。★ 浏览器不会告诉一个页面它挑的目录在哪 —— `webkitRelativePath` 是相对的，
`showDirectoryPicker()` 只给得出一个名字。★ 两个规矩跟着来：**只给本机**（窗口开在
服务端桌面上，请求在宿主上起了个进程），而**关掉选择器不是失败** —— 那是一个人说
「现在不要」。

---

## 11. 自省

### 11.1 `GET /v2/agents`

```json
{
  "defaultAgent": "main",
  "agents": [
    { "id": "main", "description": "…", "model": { "provider": "deepseek", "model": "…" },
      "subagents": ["researcher"], "tools": ["read_file", "task"] }
  ]
}
```

★ **没有「谁能当会话 agent」的名单** —— 每个声明了的 agent 都能当，嵌套多深都行。

★ **也没有「归属根」与「深度」**：一个 agent 可以同时是别人的下属、又是自己的入口
—— 「它在谁的队伍里第几层」没有唯一答案。

### 11.2 `GET /v2/agents/{agentId}`

单个 agent 详情，附它的**生效工具表**：

```json
{ "id": "researcher", "…": "…",
  "effectiveTools": [ { "name": "read_file", "tier": "allow" },
                       { "name": "write_file", "tier": "ask" } ] }
```

★ **不得在 API 层复制一套装配逻辑去算 `effectiveTools`**：真实来源是装配器暴露的
只读查询，两处逻辑必然漂移（`docs/architecture.md` §「API 层」那条规矩）。

★ **默认档位是 `ask`** —— 一个没人点名的工具要问。例外是文件在自己的 `allows:`
里点名的那些，和代码知道的内置读取器。

### 11.3 `GET /v2/mcps`

MCP 服务器与它们的工具。★ 挂载即授予：收窄 MCP 工具的地方是**服务器自己的**
`tools:` 过滤器，那是**按服务器**的，对所有挂载它的 agent 共享（这是**已拍板**的
代价，不是待办）。

### 11.4 `GET /v2/skills`

这一代挂着的 skill 清单。★ `description` 是**模型选不选它**的唯一依据 —— 只有名字
的清单对模型没用，对用户也没用。

★ 清单与 `GET /v2/config` 里的 `skills` 是**同一份数据** —— 两处各扫一遍目录，清单
就会在「配置变了」与「目录变了」之间各说各话。

### 11.5 `GET /v2/config`

配置视图：`version`（配置指纹）、`deferred`（降级/未生效声明）、`policy`（档位表）、
`tools`、`agents`、`providers`、`skills`、`server`。

★ `deferred` 把配置层的降级声明暴露给前端 —— 以前这些**只打到启动日志**，网页上看
不见，是排障盲区。

★ `providers[].hasApiKey` 只返回布尔值，**绝不返回密钥本体**。

### 11.6 `GET /v2/health` 与 `GET /v2/runtime`

- `/v2/health`：`{"status":"ok","configVersion":"…","startedAt":"…","uptimeSecs":…}`。
  ★ 它**不返回二进制版本号** —— 那是另一回事。
- `/v2/runtime`：运行时快照与**一致性巡检**（pending / checkpoint 的四象限）。
  ★ `orphan`（有 pending 无 checkpoint，审批无法恢复）与 `ownerless`（有 checkpoint
  无 pending）都是需要人工介入的状态。

★ 这几个端点在**没有配置源**时返 **503**（`api/v2_introspect.go:31` 的
`requireConfig`）—— 它们是自省用的，而运行时端点在没有配置源时照样工作。

---

## 12. 配置与密钥的写入面

### 12.1 `/v2/settings`（6 条）

| 方法 | 路径 | 说的是什么 |
|---|---|---|
| `GET` | `/v2/settings` | 配置树的清单（分区 + 每个分区的文件） |
| `GET` | `/v2/settings/item?path=` | 读一个配置文件 |
| `POST` / `PUT` | `/v2/settings/item` | 写一个配置文件 |
| `DELETE` | `/v2/settings/item?path=` | 删一个配置文件 |
| `POST` | `/v2/settings/validate` | 校验一份草稿（不落盘） |

| 情形 | 状态码 | `code` |
|---|---|---|
| 路径不在配置树里 | 400 | `config_path_invalid` |
| 写了已退休的 `api_key:` 字段 | 400 | `config_key_field_retired` |
| 写入会破坏继承链的合并 | 422 | `config_would_break_merge` |
| 并发写冲突 | 409 | `config_write_conflict` |

★ 写的**闸门**与**热重载**的细节在 [config-design.md](./config-design.md)，这里只说
接口面。

★ **`api_key:` 那个字段已经退休**：密钥的位置由**命名约定**给出
（`LLM_<PROVIDER>_API_KEY` / 钥匙串 `llm:<provider>`），见
[secrets-design.md](./secrets-design.md)。服务端**拒绝**这个字段，并说清哪一条取代了它。

### 12.2 `/v2/secrets`（4 条）

| 方法 | 路径 | 说的是什么 |
|---|---|---|
| `GET` | `/v2/secrets` | 两个命名空间的清单 |
| `PUT` | `/v2/secrets/:group/:name` | 写一把钥匙 |
| `DELETE` | `/v2/secrets/:group/:name` | 删一把钥匙 |
| `DELETE` | `/v2/secrets/:group` | 删一个组 |

★ **清单永不含值**：它只说一把钥匙在哪、现在生效的是哪一把。★ 写进去的值**不回**，
一个回显密钥的响应会把密钥放进浏览器的内存、devtools 的日志和一张截图里。

★ **组在路径里**，所以「全删」由请求本身限定范围。

★ 没有可用后端时返 **503 `secret_unavailable`**。

---

## 13. 错误模型

### 13.1 错误体

```json
{ "error": "session not found", "code": "thread_not_found", "retryable": false }
```

- `error` 是给人看的**散文**；
- `code` 是**稳定的机器可读**的 ⇒ **机器只认 `code`**；
- `retryable` 只在端点自己的分类说「重试有用」时为真（配额墙不是，429 是）。

★ `cause` 是**非导出字段**：「日志里有、客户端看不到」由 `encoding/json` 只序列化导出
字段这条规则保证，不靠文档约定。★ error 里常带绝对路径，回给客户端对用户没帮助，只
会在转贴 issue 时泄露本地目录结构。

★ **请求级失败**走 HTTP 状态码 + 错误体；**运行级失败**（模型报错、超时、抢占）走
流内 `RUN_ERROR` —— 200 早已发出，只能走事件。

### 13.2 码表

| `code` | HTTP | 触发 |
|---|---|---|
| `invalid_body` / `empty_message` / `command_not_runnable` / `file_required` / `invalid_file_name` / `agent_not_found` / `config_path_invalid` / `config_key_field_retired` | 400 | 请求不合法 |
| `thread_not_found` | 404 | 会话不存在或 id 非法 |
| `project_not_found` | 404 | 项目不存在 |
| `session_busy` / `interrupt_mismatch` / `no_pending_interrupt` / `no_checkpoint` / `turn_superseded` / `turn_start_timeout` / `config_write_conflict` | 409 | 状态冲突 |
| `run_not_live` | 404 | ★ 挂回去的那一轮没了（**不是 409**：它不是冲突，是「去读历史」） |
| `file_too_large` | 413 | 上传超限 |
| `config_would_break_merge` | 422 | 写入会破坏继承链合并 |
| `forbidden_host` / `forbidden_origin` | 403 | 主机守卫 |
| `config_unavailable` | 503 | ★ 没有可用的配置源（自省、项目文件、配置读写的入口） |
| `directory_picker_unavailable` / `directory_picker_remote` / `directory_picker_failed` | 503 / 403 / 500 | 目录选择器 |
| `secret_unavailable` | 503 | 没有可用的密钥后端 |
| `internal` | 500 | 未分类 |

★ `session_busy` **可重试**（`api/v2_types.go:124`）：冲突会自己消失 —— 那一轮结束
了，或者审批被答了。★ 它从前叫 `agent_busy`，那个名字**是错的**：它的三个调用点
（切 agent、压缩、跑一轮）守的都是**会话**，而并行委派意味着几个 agent 同时忙是
合法的 —— 把 `agent_busy` 读成「这个 agent 在忙」的客户端会得出恰恰相反的结论。

### 13.3 ★ `model_bad_request` 里最容易踩的一种：历史不合法

`model_bad_request` 大多来自端点对**请求参数**的拒绝，但有一种是服务端自己造成的 ——
历史里有 `tool_calls` 却没有对应的 tool 消息：

```
An assistant message with 'tool_calls' must be followed by tool messages
responding to each 'tool_call_id'. (insufficient tool messages following
tool_calls message)
```

★ **这不是用户能修的**：历史由运行时归档落盘，它无条件 append。一旦某个工具**没跑出
结果**就结束了（未知工具、abort、轮次被抢占、迭代上限耗尽），那条 assistant 消息就
成了孤儿，而**此后每一轮都 400** —— 重开会话、换模型、改配置都救不回来。

⇒ 修在**读取侧**（`agentkit/run/history.go:10` 的 `normalizeHistory`）。★ 一次修同时
做到三件事：治好新的一轮、治好**已经坏掉**的老会话、不必去逐个堵产生孤儿的位置
（那不止一处，每堵一处都会再冒出新的）。★ 守着的测试是
`agentkit/run/input_history_test.go:61` 与
`agentkit/run/input_history_contract_test.go:99`。

★ **落盘历史不被改写** —— 规范化只作用于发出去的那份副本。★ 所以修复是「每次读都
重做一遍」，而不是一次性掩盖证据：那批孤儿消息仍留在会话里，导出问题时看得到。

★ 补出来的消息**只说事实、不猜原因**（`"grep" did not run: the turn ended before
its result came back`）—— 模型会照着编出来的原因往下推，说「被拒绝了」它就去找
绕过路线。

### 13.4 ★ 5xx 的日志必须带 cause

★ 5xx 的**主要出口**是 `v2FailInternal(c, err)`：客户端只拿到 `"internal error"`，
真实原因只进日志。★ 描述在本地目录里查得到，**原因是唯一查不到的那半** —— 而三种
可能的处置完全不同：文件损坏、目录没权限、路径类型不对。

> ★ **例外**：配置类端点在没有可用配置源时返 **503 `config_unavailable`**，它走的是
> `v2Fail`（不带 cause、不记 5xx 日志）—— ★ 因为那时根本没有「内部错误」可带。位置：
> `api/v2_introspect.go:33`、`api/v2_project.go:51`、`api/v2_settings.go:57`。

★ `cause` 是 `errBody` 的**非导出字段**（`api/v2_types.go:78`）：「日志里有、客户端
看不到」由 `encoding/json` 只序列化导出字段这条规则保证，不靠文档约定。★ error 里常
带绝对路径，回给客户端对用户没帮助，只会在转贴 issue 时泄露本地目录结构。

---

## 14. 提案（**未实施**）

★ 下面每一条都**未实施** —— 它们要么是还没接上的一根线，要么是一条已知但刻意没有
堵上的洞。**它们不是现状**。

| # | 项 | 现状 | 要定的 |
|---|---|---|---|
| 1 | AG-UI 标准的 `usage[]` | SDK 的 `events.WithUsage` 在本仓库零调用；用量只在 `/messages` | 做不做 —— 它与 `context_usage` 是**两个轴**（逐 (provider, model) 的厂商记账 vs 逐 agent 的占用） |
| 2 | 「是谁答的审批」 | 帧里带着 `approved` 与 `reason`，但没有答的人 | 说「谁」就得先有「谁」的身份 |
| 3 | 会话的公告通道**回放** | 通道没有窗口，过去只归扫描 | 要不要给后来者补一段 |
| 4 | 公告通道的**终态帧** | 没有：它在客户端离开时结束 | 要不要一个「这个会话结束了」的帧 |
| 5 | `POST /v2/run` 的**流内用量** | 用量在 `/messages` | 与第 1 条同源 |
| 6 | `/v2/sessions` 的**分页** | 一次全给 | 会话多了以后要不要游标 |

★ **第 3 条与第 4 条是一件事的两面**：通道是「活的通知」，而一个**晚到的**界面需要
「过去」。★ 现在两者分工：过去归扫描（`/messages` + `/runs`），通知归通道。★ 补回放
会让「晚到」与「一直在听」这两种界面拿到同一种待遇，代价是第三份要裁剪的东西。

★ **第 6 条的代价**是一次全量列会话要重开每个会话的文件 —— 而标题已经缓存了，
所以它现在是「读一个目录」，不是「读 n 个文件」。

---

## 附录 A：本文引用的代码位置

★ 每一行都打开读过。行号会随代码漂移；漂了以后以文件与符号为准。

### A.1 路由与错误

- `api/v2_files.go` — `registerV2Routes`(:40)、扫描的路由只读(:57)、公告通道的路由(:64)、
  `v2UploadFiles`(:138)、`maxUploadBytes`(:136)、`v2RawFile`(:291)、`isPreviewImage`(:361)、
  `isTextualMime`(:375)、`listWorkspace`(:438)
- `api/v2_types.go` — 错误码(:28)、`codeSessionBusy`(:48)、`codeRunNotLive`(:58)、
  `errBody`(:63)、`cause`(:78)、`v2Err`(:82)、`v2FailInternal`(:99)、`v2FailErr`(:108)、
  `threadState`(:151)、`threadMeta`(:159)、`pendingView`(:193)、`threadDetail`(:212)、
  `runView`(:233)、`runsView`(:256)、`usageView`(:292)、`contextUsageView`(:306)、
  `messagesView`(:341)、`toPendingViews`(:351)、`toPendingView`(:363)、`toUsageView`(:383)
- `api/guard.go` — 主机守卫(:46)、`Reject`(:61)、`sameSite`(:93)、`mutating`(:122)、
  `LoopbackOnly`(:136)
- `api/v2_run.go` — `runInput`(:28)、`runAttachment`(:91)、`resumeEntry`(:108)、
  `v2Run`(:122)、`isAttachRequest`(:224)、`attachRun`(:241)、`startRun`(:291)、
  `lastUserText`(:371)

### A.2 会话与项目

- `api/v2_sessions.go` — `v2CreateSession`(:29)、`v2ListSessions`(:93)、`v2GetSession`(:118)、
  `v2RenameSession`(:155)、`v2SwitchAgent`(:210)、`resolveAgentID`(:246)、
  `ensureAgentIdle`(:259)、`v2DeleteSession`(:288)、`v2Messages`(:317)、
  `sessionUsage`(:390)、`v2Abort`(:434)、`v2SessionRuns`(:471)、`v2SessionRun`(:492)、
  `metaOf`(:527)、`stateOf`(:559)
- `api/v2_projects.go` — `codeProjectNotFound`(:20)、`projectView`(:29)、`projectScope`(:44)、
  `v2Projects`(:57)、`projectSnapshot`(:107)、`v2CreateProject`(:143)、`v2UpdateProject`(:183)、
  `v2DeleteProject`(:225)
- `api/v2_dirpick.go` — 三个码(:31)、`v2PickDirectory`(:59)、`pickDirectory`(:93)、
  `localCaller`(:115)
- `api/v2_project.go` — `v2ProjectFiles`(:49)、`config_unavailable`(:51)、
  `projectRootOf`(:95)、`listProjectFiles`(:121)

### A.3 自省、配置与密钥

- `api/v2_introspect.go` — `requireConfig`(:31)、`config_unavailable`(:33)、
  `modelView`(:97)、`agentView`(:108)、
  `toolView`(:132)、`v2Agents`(:147)、`v2AgentDetail`(:160)、`agentViews`(:183)、
  `effectiveTools`(:268)、`policyTools`(:287)、`SkillView`(:337)、`v2Skills`(:350)、
  `serverView`(:358)、`configView`(:369)、`v2Config`(:401)、`v2Health`(:485)、
  `v2Runtime`(:502)
- `api/v2_settings.go` — 四个码(:13)、`config_unavailable`(:57)、
  `v2SettingsManifest`(:91)、`v2SettingsItem`(:109)、
  `v2SettingsSave`(:134)、`v2SettingsDelete`(:154)、`v2SettingsValidate`(:173)
- `api/v2_secrets.go` — `codeSecretUnavailable`(:32)、`secretItemView`(:48)、
  `v2Secrets`(:89)、`v2SecretWrite`(:151)、`v2SecretDelete`(:192)、`v2SecretGroupDelete`(:231)
- `api/v2_mcps.go` — `MCPServerView`(:23)、`MCPToolView`(:32)、`v2MCPServers`(:74)
- `api/commands.go` — `commandKind`(:16)、`commandAction`(:23)、`commandSpec`(:38)、
  `v2Commands`(:122)、`expandCommand`(:140)、`splitCommandName`(:171)

### A.4 会话的公告通道

- `agentkit/run/announce.go` — `sessionAnnouncer`(:27)、总线按订阅者建(:42)、
  `subscribe`(:54)、`release`(:59)、`publish`(:77)、`Announce`(:93)、
  `ReleaseAnnounce`(:102)、`AnnounceWatchers`(:115)、`announceApprovalPending`(:141)、
  `announceApprovalResolved`(:168)、`announceRunStarted`(:190)
- `agentkit/run/bridge.go` — `markTurnDone`(:73)、`releaseRound`(:103)、`clearTurn`(:144)、
  `loopExited`(:190)、`if stale`(:201)、`st.markTurnDone`(:211)、
  `writeHeldApproval` 调用(:219)、`closeApprovalRow` 调用(:220)、`closeOpenRow` 调用(:221)、
  宣布待批(:232)、`publisher.Close()`(:237)、`releaseRound` 调用(:244)、
  `closeApprovalRow`(:255)、`closeOpenRow`(:277)、`writeHeldApproval`(:308)
- `agentkit/run/runtime.go` — `busy` 的判定(:304)、
  `releaseClaim`(:409)、`runClaimedTurn`(:440)、`announceRunStarted` 调用(:469)、
  `Resume`(:622)、`announceApprovalResolved` 调用(:676)、`StatusOf`(:897)、`LiveRun`(:1011)
- `agentkit/run/turn.go` — `pin`(:101)、`holdPending`(:228)、`takePending`(:233)、
  `holdsSlot`(:262)、`setRow`(:357)、`closeRow`(:390)
- `contract/runevent.go` — `RunEventKind`(:13)、`RunEventApprovalPending`(:59)、
  `RunEventApprovalResolved`(:68)、`RunEventRunStarted`(:75)、`RunEvent`(:88)
- `contract/contract.go` — `PendingReasonToolCall`(:111)、`PendingInterrupt`(:114)、
  `InterruptDetail`(:125)
- `transport/transport.go` — `RenderSession`(:115)、`emitApprovalPending`(:305)、
  `emitApprovalResolved`(:342)、`emitRunStarted`(:358)
- `api/v2_session_events.go` — `sessionPoll`(:20)、`v2SessionEvents`(:40)
- `web/src/lib/session-events.ts` — `readInterrupt`(:60)、`parseSessionFact`(:80)、
  `applySessionFact`(:126)
- `web/src/lib/store/actions.ts` — `watchSession`(:935)、`applySessionFact`(:988)、
  `followRun`(:1009)、`recoverRound`(:770)

### A.5 实测与传输层（§5.1、§5.2、§13.3、§13.4）

- `approval/approval.go` — `gob.Register`(:18)、`gob.Register`(:19)
- `approval/approval_test.go` — `TestDecisionSurvivesGob`(:9)
- `agentkit/build/gate_test.go` — `TestApprovalInfoSurvivesGob`(:84)
- `agentkit/emit/emit.go` — `describe(ic.Info)`(:444)
- `agentkit/emit/interrupt_test.go` — `TestTheInterruptDescribesTheApprovalInfo`(:14)
- `agentkit/run/history.go` — `normalizeHistory`(:10)
- `agentkit/run/input_history_test.go` — `TestNormalizeHistoryFillsTheMissingResult`(:61)
- `agentkit/run/input_history_contract_test.go` — `TestNormalizeHistoryDoesNotFabricateResults`(:99)
- `web/src/lib/sse.ts` — 吃掉一个可选空格(:43)
- `web/scripts/interop-check.ts` — 无空格分帧的计数(:34)
- `transport/transport.go` — `emit`(:220)、`reasoningRole`(:398)、
  `NewToolCallResultEvent`(:445)
- `transport/transport_test.go` — `ValidateSequence`(:93)
- `api/v2_test.go` — 路由优先级(:37)
