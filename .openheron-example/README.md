# openheron 配置示例

把这些目录复制到项目根的 `.openheron/` 即可：

```bash
cp -r .openheron-example/{agents,llm,policy,skills} /your/project/.openheron/
```

| 目录 | 内容 |
|---|---|
| `agents/main.md` | agent 声明 —— frontmatter（`model` / `max_iterations` …）+ **正文即提示词** |
| `llm/openai.yaml` | 厂商档案 —— `base_url` / `api_key` / `models[]` / 思考档位表 |
| `policy/permissions.yaml` | 审批档位 —— `ask` / `deny`（**未点名的一律放行**） |
| `server/config.yaml` | 进程参数 —— 监听地址 / 目录 / 超时 / **不进热更新** |
| `skills/<name>/SKILL.md` | eino 原生 skill 约定 |
| `tools/<tool>.yaml` | 外部工具与知识库 —— **文件在 = 开启**（这份样例不带，外部工具默认全关） |

`state/`（会话与 checkpoint）在首次启动时自动创建，**不要**提交。

## 哪些改了会自动生效

| 目录 | 热更新 | 原因 |
|---|---|---|
| `llm/` `agents/` `policy/` `tools/` | ✅ | 下一轮就用新配置 |
| `server/` | ❌ | 配的是进程自身，改了要重启 |

改坏配置**不会让进程死** —— 沿用上一份并记一条日志。

## 三条会让人卡住的规则

1. **`api_key` 写 `${ENV}`** —— 环境变量未设**不会**阻塞启动，只在启动日志里
   记一条提醒；等到真正请求该端点时才返回「无权限访问」(401)。也可用
   `@file:/path` 读文件（路径写错则启动期报错）。**不要把密钥写进本文件**，
   它通常要入版本库。

2. **`model` 必须写成 `<provider>/<model>`** —— `provider` 是 `llm/` 下的文件名，
   `model` 是该文件 `models[]` 里声明的 `id`。解析不到会在启动期报错，
   **不会静默退化到别的模型**。

3. **时长必须带单位** —— `timeout: 5m`，写 `timeout: 300` 会被拒绝
   （否则 `300` 会被当成 300 纳秒）。

## 思考档位

档位名由 `llm/*.yaml` 的 `models[].reasoning` 定义，agent 只写档位名：

```yaml
# agents/main.md
reasoning: high
```

**同一个 `high` 在不同端点发的字段可能完全不同** —— 这正是词汇表存在的理由。
agent 写了一个该模型没有的档位时**不拦启动**，但会在启动日志里说一声
（「这个档位的思考参数没发出去」）—— 那是唯一可见的线索。

## 子 agent（多角色）

`agents/main.md` 的 `subagents: [researcher, coder, reviewer]` 声明拓扑。子 agent
与其它 agent **是同一格式的同一个东西** —— 差别只是文件名。

每个子 agent 都会被**逐个装配**自己的审批与工具过滤中间件（不能指望父 agent
传播），且各自按 `model:` 建自己的模型实例。

| 开关 | 位置 | 作用 |
|---|---|---|
| `agent.max_depth` | `server/config.yaml` | 嵌套深度上限（入口是第 0 层）。默认 `1` = 只有入口 agent 能委派 |
| `disabled_tools` | `agents/*.md` | 只影响这一个 agent。与全局 `deny` **分层保存，永不合并** |

声明的拓扑超出 `max_depth` 时**启动期报错**，不会被静默截断。

### 出厂样例的四个角色

| 角色 | 干什么 | 能写文件吗 |
|---|---|---|
| `main` | 编排：分派、汇总 | 否（默认禁用了写与执行） |
| `researcher` | 只读检索：定位实现、追调用链 | 否 |
| `coder` | 按结论改文件、跑测试 | **能** |
| `reviewer` | 读改动、挑问题 | 否 |

「谁能写」靠**两层**控制，缺一不可：

- **外层** `policy/permissions.yaml` 的 `deny` —— 谁都别想用；
- **内层** 各 agent 自己的 `disabled_tools` —— 只影响这一个。

★ 外层 `deny` 优先于内层 `disabled_tools`：这是子 agent **无法放松**的唯一保证。
  两层是分层保存的，永不合并 —— 合并就丢掉了「谁在外层」。

### 并行委派

几件**互不依赖**的事，可以一条消息里发多个 task 调用，让它们同时跑：

- 「A 模块和 B 模块各自实现一下」→ 两个 `coder` 并行；
- 「先查 A 的实现，再查 B 的实现」→ 两个 `researcher` 并行（都只读，不互相踩）；
- 「改完之后审一遍」→ **串行**，`reviewer` 必须等 `coder` 改完。

★ 并行只对**互不依赖**的任务成立。前一步的输出是后一步的输入时，并行只会拿到
  一堆基于旧世界的结论。

★ 多个子 agent 同时要人批准时，会**同时**弹出几张审批卡 —— 把它们**一起**答掉；
  只答一张，另外几张会重新要一次。

★ 并行时每张审批卡会标出**是谁**要的（「Requested by coder」）—— 那是从框架的
  地址链推出来的，不是猜的。详见 `docs/multi-agent-design.md`。

## 审批默认档位

**未列名的工具兜底是 `allow`（直接放行）** —— 策略文件只有 `ask` / `deny` 两列，
没点名就是放行。所以新增一个工具后它默认能直接用；想让它**每次都要人点头**，
去 `policy/permissions.yaml` 的 `ask` 里加名字；想让它**完全不能用**，加进 `deny`。

★ 外层 `deny` 优先于内层 `disabled_tools` —— 那是子 agent 无法放松的唯一保证。
  要收窄**某一个** agent 的能力，用它自己的 `disabled_tools`，别用全局策略。
