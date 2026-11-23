# 契约：密钥设置窗口的本地化（设置 → 密钥 页签）

- 版本：v1.1（v1 + 三处歧义裁定，见 §10）
- 日期：2025-xx-xx
- 契约文件：`docs/agile/contract.md`（唯一事实来源）
- 涉及界面：`web/src/components/settings/secrets-panel.tsx`、`web/src/lib/secrets.ts`、词条表 `web/src/lib/locales/parts/{lib,settings}.ts`

---

## 1. 背景与目标

「设置 → 密钥」页签的界面文案由两处来源拼成：一处是词条表（已双语），一处是**服务端下发的英文**（分组名 `label`、错误句 `error`）。中文界面因此出现英文残留与半成品翻译。

**目标**：密钥窗口内**所有面向用户的文字**都随界面语言呈现；服务端继续保持英文表述（运行时的语言不动），由前端负责翻译与回退。

**已确认的决策（用户拍板）**
1. 分组名的翻译**由前端负责**；服务端 `label` 保持英文原样，作为英文回退与未知分组的兜底。
2. 服务端错误保持英文；**前端按错误码适配翻译**为界面语言；未映射的错误码回退显示服务端英文原句。

## 2. 范围

### 本次做
- 分组显示名（`Providers` / `MCP servers`）本地化。
- 密钥面板内一切由服务端英文派生的文字（分组名、错误句）本地化。
- 词条表补全与措辞统一（`secrets.*` / `settings.secrets.*`）。

### 本次不做
- 服务端任何改动（错误信封、错误码、分组 `label` 全部不动）。
- 密钥条目名（`deepseek`、`MCP_FILESYSTEM_TOKEN`）与来源串（`keyring:llm:deepseek`）的本地化 —— 它们是标识符，不是句子。
- 密钥窗口以外的任何页签/组件的翻译问题。
- 服务端错误句本身的翻译（仍由服务端产出英文）。

---

## 3. 用户故事

### S1 分组显示名随界面语言（P0）

**作为**使用者，**我希望**密钥面板的分组标题是我正在读的语言，**以便**我不用在中文界面里辨认英文分组名。

验收标准
- 语言=zh 时，面板出现「服务商」与「连接器」两个分组标题。
- 语言=zh 时，面板**不出现** `Providers`、`MCP servers`（大小写不敏感）。
- 语言=en 时，面板出现 `Providers` 与 `MCP servers`。
- 分组标题随语言切换**立即**变化（不需重新打开页签）。

本次不做：不改服务端 `label` 字段的值。

### S2 「移除全部 {group}」按钮使用本地化分组名（P0）

验收标准
- 语言=zh 时，两个按钮文案为「移除全部 服务商」「移除全部 连接器」。
- 语言=en 时，按钮文案为 `Remove all Providers`、`Remove all MCP servers`。
- 任何语言下都**不得**出现 `mcp servers`、`remove all providers` 这类被小写化的名字（`MCP` 等缩写保持原大写）。

### S3 删除整组的确认弹窗本地化（P0）

验收标准
- 弹窗正文中的分组名（`{group}`）与「不受影响的那一组」（`{other}`）均为当前语言文案。
- ★ `{group}` 填**分组名**（服务商 / Providers），`{other}` 填**另一组的范围词**（连接器密钥 / MCP server secrets）——两者不同类，句式照旧。（v1.1 澄清。）
- 语言=zh 时，弹窗正文不含 `Providers`、`MCP servers`、`provider secret(s)`、`MCP server secret(s)`。
- 弹窗仍同时给出**数量**与**范围**（两个语义不得因翻译丢失）。

### S4 错误句按错误码本地化（P0）

验收标准
- 服务端返回 `code=secret_unavailable` 时，语言=zh 显示中文错误句，且**不显示**服务端英文原句。
- 服务端返回 `code=invalid_body` 时，语言=zh 显示中文错误句，且不显示服务端英文原句。
- 服务端返回**未映射**的 `code` 时，界面**原样显示**服务端 `error` 英文句（回退路径可用）。
- 非 ApiError 的失败（网络/解析异常）时，语言=zh 显示本地化的通用失败句，不显示裸英文异常文本（如 `Failed to fetch`）。
- 语言=en 时，所有错误句为英文。

### S5 词条表补全与措辞统一（P1）

验收标准
- `secrets.*` 与 `settings.secrets.*` 的每个键 `zh` 非空。
- 中文文案中**不残留**未翻译的英文单词（专有名词 `MCP` 除外）；当前残留 `"provider 密钥"` 必须修正。
- 四个范围词（provider/mcp × 单/复）在中英文下各自成对且互不混用。
- ★ 范围词是**嵌入句中的片段**（句式为 `Remove {count} {scope}?`），因此**英文侧一律小写**（`provider secret(s)` / `MCP server secret(s)`）；它们不是独立标题，不要求首字母大写。（v1.1 裁定：英文范围词保持小写，A8 不再要求其与其它词条的大小写一致。）
- ★ 中文无单复数变化，故 `…One` 与 `…Many` 的中文取值相同是**允许**的；但四个键必须都存在。

### S6 回归与证据（P0）

验收标准
- 既有 `web/test/e2e/secrets.spec.ts` 全部通过；其中受文案变更影响的断言已同步为新文案（**不得**为了让测试通过而放宽断言）。
- 新增针对 S1–S4 的自动化验证（单元测试 + 端到端测试各覆盖其可测部分）。
- `web` 的构建通过（`bun run build`）。

### S7 设计文档与实现同步（P1）

**作为**维护者，**我希望** `docs/secrets-design.md` 描述的界面与现行契约一致，**以便**后来者不会照着一份已过时的文档把分组名改回服务端直出。

验收标准
- `docs/secrets-design.md:241` 的界面示意中按钮文案改为 `Save key`（与 `settings.secrets.setValue` 的 en 值逐字一致）。
- `docs/secrets-design.md` 中「组的标签由服务端给，客户端不自带一份名单」这一条**改为**：显示名由前端按 `group.id` 本地化，服务端的 `label` 是英文回退与未知 `id` 的兜底；并写明这是一条**显示层**的规则，不是分区表的第二份真相（分区表仍归服务端）。
- 文档中分组标题的示意能看出「随界面语言变化」（英文界面为 `Providers` / `MCP servers`，中文界面为「服务商」/「连接器」）。
- 文档中删除确认的示例句保持英文原样，并注明中文界面显示本地化文案。
- 文档中 JSON 载荷示例的 `label` 仍为服务端英文原值（服务端未改）。
- 文档改动**只限**上述几处；不重写其它章节，不新增章节。

### S8 同类漂移的补正（P1）

**作为**维护者，**我希望**文档里剩下的、与现行实现不一致的描述也被改正，**以便**这份文档可以直接当实现说明书读。

验收标准
- `docs/secrets-design.md` §5.3「界面规矩」表中「文案 | **全部英文**（仓库规矩）」一条改为现行事实：**界面文案随界面语言**（zh / en），而**服务端**仍产出英文（错误句、分组 `label`）。该表须与 §5.1 已写的本地化规则不矛盾。
- §5.1 的 ASCII 示意图与现行实现逐字对齐：两行分别是「Stored in」（原样显示 `item.source`，如 `keyring:llm:deepseek`）与「In effect」（经 `sourceLabel()` 显示为句子，如 `the keyring entry llm:deepseek`），警告行显示实际句子 `The environment variable {envVar} wins — the stored key is not in effect.`（含变量名）。
- §5.1 引用的 `secrets-panel.tsx` 行号与现行文件一致（面板函数实际起止行以文件为准）。
- 只改上述三处及其必要的紧邻措辞；不重写其它章节，不新增章节。

### S9 最后两处引用与措辞（P2）

验收标准
- `docs/secrets-design.md` §5.4 中对 `web/src/lib/api.ts` 的行号引用按现行文件更正（`ApiError` 类与 `ApiErrorBody` 的实际行号）。
- §5.3「`shadowed` 必须看得见」一行的警告句改为**逐字**引用现行词条 `secrets.status.shadowed`（含 `{envVar}` 占位形式），与 §5.1 的示意图一致。
- 只改这两处；不重写其它章节，不新增章节。

---

## 4. 接口形状（本次不变）

### 4.1 分组列表

`GET /v2/secrets` → `200`

```jsonc
{
  "groups": [
    {
      "id": "llm",            // 稳定标识符：本地化的依据
      "label": "Providers",   // 服务端英文名：英文回退 / 未知 id 的兜底
      "items": [
        {
          "name": "deepseek",
          "stored": true,
          "source": "keyring:llm:deepseek",
          "effective": "env:LLM_DEEPSEEK_API_KEY",
          "shadowed": true,
          "envVar": "LLM_DEEPSEEK_API_KEY"
        }
      ]
    },
    { "id": "mcp", "label": "MCP servers", "items": [ /* … */ ] }
  ]
}
```

**字段语义**：`id` 是分组的**稳定标识**（`llm` / `mcp`），是界面本地化与作用域判断的依据；`label` 是服务端的**英文**名，只在界面语言为英文、或 `id` 未知时显示。

### 4.2 错误信封

`GET/PUT/DELETE /v2/secrets*` 失败 → `{ "error": "<英文>", "code": "<机器可读>", "retryable": bool }`

**语义**：`code` 稳定且机器可读，界面按 `code` 分支/翻译；`error` 是英文原句，仅在 `code` 未被映射时原样显示。

### 4.3 本窗口相关的错误码

| `code` | 服务端状态码 | 含义 | 界面文案（en / zh） |
|---|---|---|---|
| `secret_unavailable` | 503 / 500 | 本机密钥环不可用 | "The keyring is not available on this machine." / 「本机密钥环不可用。」 |
| `invalid_body` | 400 / 404 | 请求不合法，或分组/名称不存在 | "The request was not valid." / 「请求不合法。」 |
| `unknown`（客户端合成） | — | 响应不可解析 | "Something went wrong." / 「出了点问题。」 |
| `network`（客户端合成） | — | 连不上服务端 | "The server could not be reached." / 「无法连接到服务端。」 |

**回退规则**：表中没有的 `code` → 原样显示服务端 `error`；**不翻译、不加前缀、不吞掉**。

---

## 5. 数据结构

- **分组显示名** = 由 `group.id` 查词条表得到；`group.id` 不在表内 → 用 `group.label` 原样显示。
- **显示名不做大小写变换**：任何语言下都不得对分组名调用 `toLowerCase()`（`MCP servers` → `mcp servers` 是缺陷）。
- **作用域词**（provider / mcp，单数与复数）由 `group.id` + 条目数量决定，与显示名相互独立。
- **条目名与环境变量名**原样显示，不翻译。

---

## 6. 词条契约

新增键（`en` / `zh` 为**必须**逐字采用或经用户确认后采用）：

| 键 | en | zh |
|---|---|---|
| `secrets.group.llm` | Providers | 服务商 |
| `secrets.group.mcp` | MCP servers | 连接器 |
| `secrets.error.secretUnavailable` | The keyring is not available on this machine. | 本机密钥环不可用。 |
| `secrets.error.invalidBody` | The request was not valid. | 请求不合法。 |
| `secrets.error.unknown` | Something went wrong. | 出了点问题。 |
| `secrets.error.network` | The server could not be reached. | 无法连接到服务端。 |

既有键的**修正**（编号为验收依据，见 S5）：

| 键 | 现状 | 改为 |
|---|---|---|
| `secrets.scope.providerOne` | zh「provider 密钥」 | zh「服务商密钥」 |
| `secrets.scope.providerMany` | zh「provider 密钥」 | zh「服务商密钥」 |
| `secrets.scope.mcpOne` | zh「连接器密钥」 | 保持（英文侧 `MCP server secret`） |
| `secrets.scope.mcpMany` | zh「连接器密钥」 | 保持（英文侧 `MCP server secrets`） |
| `secrets.source.none` | en「nowhere — no key is in effect」 | en「no source — no key is in effect」/ zh「无来源 —— 没有生效的密钥」 |
| `secrets.remove.message` | zh「…，{other}保持不变。」 | zh「移除 {count} 个 {scope}？仅 {group} 分组受影响，{other} 保持不变。」（补空格） |
| `settings.secrets.setValue` | en「Set value」/ zh「设置值」 | en「Save key」/ zh「保存密钥」 |

> `secrets.scope.*One/Many` 在中文下取值相同是**允许**的（中文无单复数变化）；但四个键必须都存在，且不得再有英文残留。

---

## 7. 错误与边界处理

| 情形 | 要求 |
|---|---|
| `group.id` 未知 | 显示服务端 `label` 原样；不崩溃、不显示空标题 |
| `group.label` 缺失/为空 | 显示本地化分组名；不显示空白标题 |
| `items` 为空数组 | 显示既有空态文案（`settings.secrets.empty`），不变 |
| 错误 `code` 未映射 | 原样显示服务端 `error` 英文句 |
| 错误响应体非 JSON | 显示 `secrets.error.unknown` 本地化文案 |
| 网络异常（非 ApiError） | 显示 `secrets.error.network` 本地化文案，不显示裸异常文本 |
| 非 ApiError 的分类规则 | 响应体不可解析（SyntaxError）→ `secrets.error.unknown`；其余非 ApiError 失败（网络等）→ `secrets.error.network`（v1.1 裁定） |
| 服务端真的返回 `code=unknown` | 走 `secrets.error.unknown` 本地化文案（与 §4.3 表一致），服务端原句不显示 |
| 写入后错误 | 错误句显示在面板错误位（`data-testid="secrets-error"`），位置不变 |

---

## 8. 验收清单（可判定，逐条勾验）

- [x] A1 zh 面板含「服务商」「连接器」，不含 `Providers`/`MCP servers`
- [x] A2 en 面板含 `Providers`/`MCP servers`，且 `MCP servers` 首字母大写
- [x] A3 zh 按钮为「移除全部 服务商」「移除全部 连接器」
- [x] A4 zh 确认弹窗含中文分组名/范围词/数量，不含英文组名与英文范围词
- [x] A5 `secret_unavailable` / `invalid_body` 在 zh 下显示中文句、不显示英文原句
- [x] A6 未映射 `code` 显示服务端英文原句（回退生效）
- [x] A7 非 ApiError 失败在 zh 下显示本地化通用句，不含 `Failed to fetch`
- [x] A8 词条表中 `secrets.*`/`settings.secrets.*` 的 zh 无英文残留、无空值
- [x] A9 `web/test/e2e/secrets.spec.ts` 全部通过（19 passed），既有断言与新文案一致，无放宽
- [x] A10 `bun run build`（web）通过

> 核验（qa-engineer，v1.1）：A1–A8 由 `web/test/secrets.test.ts` 单元断言与 `web/test/e2e/secrets.spec.ts` 端到端断言各覆盖；A9 `bun x playwright test secrets` → 19 passed；A10 `bun run build` → ✓ built in 600ms。

---

## 9. 委派与验证分工

- **frontend-engineer**：S1–S5 的实现（分组名本地化、错误码映射、词条表修正），并自证 `bun run build` 通过。
- **qa-engineer**：S6 —— 按 §8 验收清单补/改测试并运行，给出通过/失败证据。

契约变更须回写本文件并说明影响范围；子 agent 一律以本文件为准，不得自行改动接口形状与词条文案。

---

## 10. 变更记录

- **v1.3**（S7 执行后回写的补正）：
  1. 新增 S8：同一文档内另三处与实现不符的描述（「文案全部英文」、ASCII 图两行、行号）一并改正。
  2. 新增 S9：最后一处行号引用（`api.ts`）与一处措辞逐字化。
  3. S7–S9 均已执行并回传核对位置；示意图框宽调至 116 字符、各行右框线对齐（宽度差异仅源于 `● ○ —` 的东亚歧义宽度，属渲染差异，保留原文）。
  4. 用户决定：残余风险（A5–A7 未经真机后端验证）**待观察**，本次不处理。
- **v1.2**（用户拍板：文档漂移要改；残余风险待观察）：
  1. 新增 S7：`docs/secrets-design.md` 与现行契约同步（按钮文案 + 分组名本地化规则 + 示意图）。
  2. 原「已知文档漂移（待定）」转为 S7 的验收项，漂移不再挂起。
  3. A5–A7 的端到端验证依赖伪造响应、真实后端 503/400 路径未在真机跑过——**用户决定暂不处理，列为待观察**。
- **v1.1**（前端实现后回写的裁定，均不改变接口形状）：
  1. 英文范围词保持小写（嵌入句中的片段），A8 不再要求其与其它词条大小写一致。
  2. 确认弹窗中 `{group}` = 分组名、`{other}` = 另一组的范围词，两者不同类。
  3. 明确非 ApiError 的分类：SyntaxError → `unknown`，其余 → `network`；服务端返回 `code=unknown` 时同样走 `unknown` 本地化文案。
- **已知文档漂移（不在本次范围，待定）**：`docs/secrets-design.md:241` 的界面示意仍写 `[ Set value… ]`，而按钮文案已按 S5 改为 `Save key` / 「保存密钥」。是否同步该文档由用户另行决定。
