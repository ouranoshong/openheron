# 文件系统后端设计 —— `eino-ext` local 的有界分支

> 状态：**已实现。** `agentkit/fsx` 在两处调用点替换了
> `github.com/cloudwego/eino-ext/adk/backend/local@v0.2.2`
> （`agentkit/build/snapshot.go:72`、`agentkit/build/skill.go:96`）。§1 讲的是被删掉
> 的上游代码，所以它读起来像一段历史；§5 指着现在每一块的落点。
>
> 范围：文件系统**后端**（`ls`、`read_file`、`glob`、`grep`、`write_file`、
> `edit_file`、`execute` 都经由它落盘）。不在范围内：渲染这些工具的 middleware、
> 工具描述、审批闸门。
>
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

## 1. 问题

这些工具两种边界都没有：**没有写边界**（模型给的路径过一遍 `filepath.Clean` 就落盘），
也**没有读的上限**。`ls` 和 `glob` 接受模型给的路径，而上游除了 `filepath.Clean`
之外什么也不做：

| # | 缺口 | 上游 |
|---|---|---|
| 1 | 空路径意味着 **`/`** | `local.go:39` `const defaultRootPath = "/"`，`local.go:76-78` |
| 2 | `ls` 只接受绝对路径，所以省略路径的模型永远落在 `/` 上 | `local.go:80-83` |
| 3 | `glob` **遍历整棵子树**：没有深度上限、没有结果上限、没有忽略规则、没有剪枝 | `local.go:275-306` |
| 4 | 一个读不了的目录就把整次遍历中断 | `local.go:282-287` |
| 5 | `write_file` / `edit_file` 同样无界 —— 模型写哪儿就落哪儿 | `local.go:324-385` |
| 6 | `grep` 先把**整份结果**缓冲下来再解析，然后又切一遍 | `local.go:352` `Output()` + `strings.Split` |

**真正炸的是缺口 3。** 在一台挂了 `/mnt/d` 的 WSL 机器上，一次找 `*.md` 的 `glob`
被指到了项目外面，于是它的时间花在了 Windows 回收站里：

```
failed to walk directory: readdirent /mnt/d/$RECYCLE.BIN/S-1-5-21-.../xxx: input/output error
```

这是缺口 3 加缺口 4：遍历走出了项目，途中撞上的第一个 I/O 错误把整次调用打死。

`ls` 还有它自己的两笔成本：

- **一次多余的排序。** `os.ReadDir` 本来就已经按文件名排好序；`local.go:96-98`
  又排了一遍（白白 O(n log n)）。
- **没有类型、没有大小。** `local.go:102-104` 只填了 `Path: entry.Name()`。而
  middleware 本来就只渲染 `Path`（`middlewares/filesystem/filesystem.go:570-585`），
  于是模型分不清目录和文件，只能靠更多次 `ls` 试探；一个 3 万条目的 `node_modules`
  会一次变成 3 万行上下文，而 `deep` 这条路径**没有**任何结果大小上限（见 §2.2）。

### 1.1 一个静默的零结果 bug

`agentkit/run/input.go:93` 是这么教模型的：

```
glob(pattern="<projectRoot>/**/*.go")
```

而 `local.go:300` 是拿 `req.Pattern` 去匹配**相对遍历根**的路径。绝对 pattern 永远
匹配不上。于是提示词教模型写的每一次 glob 调用都返回「没找到文件」—— 而且是安静地
返回。

## 2. 为什么只能在后端这一层改

### 2.1 `deep.Config` 只暴露了 middleware 十二个字段里的三个

`deep` 自己装配文件系统 middleware：

```go
// eino@v0.9.21/adk/prebuilt/deep/deep.go:219-231
fm, err := filesystem2.NewTyped[M](ctx, &filesystem2.MiddlewareConfig{
	Backend:        cfg.Backend,
	Shell:          cfg.Shell,
	StreamingShell: cfg.StreamingShell,
})
```

`MiddlewareConfig` 上还有 `LsToolConfig`、`GlobToolConfig`、`CustomSystemPrompt`、
`UseMultiModalRead` 和卸载（offloading）的那几个旋钮 —— 从 `deep.Config` 一个都
够不着。`localbk.Config` 更糟，它只有一个字段（`ValidateCommand`）。

### 2.2 上游已有的上限不在我们这条路径上

`large_tool_result.go` 会把过大的工具结果卸载到文件里，但它只挂在**废弃的**、
struct 式的构造函数上（`filesystem.go:208-215`），没挂在 `NewTyped` 上 —— 而
`deep` 调的正是 `NewTyped`。所以在我们这条路径上，任何一层都没有截断：后端返回
3 万条，工具把它们 join 起来，模型全读进去。

结论：要约束 `ls` 和 `glob`，就得在它们**读盘的那一层**约束。

### 2.3 为什么不是复制 middleware

| 卡死的事实 | 证据 |
|---|---|
| 它引用了 eino 的 **internal** 包，照抄编译不过 | `filesystem.go:32` `github.com/cloudwego/eino/adk/internal`（用在 `:708`、`:1252`） |
| 1,711 行生产代码，外加 2,752 行上游测试 | `filesystem.go` 1264 + `prompt.go` 270 + `large_tool_result.go` 177 |
| 它握着七个工具的**提示词文本**，中英各一套 | `prompt.go` |

复制 middleware 唯一能多买到、而后端买不到的是 `FileInfo` 的**渲染**（一个 size
列、一句更好看的截断标记）。那只是外观层，代价却是分叉工具契约和提示词 —— 比它
所改变的行为大得多的面。真到那一天该怎么做，见 §8：在这个后端之上写七个工具，
而不是复制 middleware。

## 3. 规则

三层，各管一件事：

| 层 | 管 | 永远不碰 |
|---|---|---|
| `agentkit/fsx`（后端） | **行为**：写只能落在哪、一次遍历能走多远、什么时候停、任何一次返回多少 | 模型看到的文字 |
| 工具层（上游，不改） | **契约**：工具名、参数、输出形状 | 哪些路径可达 |
| 提示词（`agentkit/build/spec.go`、`agentkit/run/input.go`） | **措辞**：告诉模型代码在哪 | 任何后端能强制的事 |

在后端里落实的规则，模型无视提示词也绕不过去；只写在提示词里的规则，绕得过。

## 4. 决策

### 4.1 分叉后端，不分叉 middleware

后端是一个文件、一个接口（`filesystem.Backend`，七个方法，
`adk/filesystem/backend.go:243-283`）。middleware 是四个文件加一套提示词。分叉小的
那个，并且让 diff 可对账：文件头写着它来自哪个上游版本。

### 4.2 常量，不是配置

`maxLsEntries`、`maxGlobResults`、`maxGlobDepth`、`maxGrepMatches` 和忽略列表都是
包内常量。到目前为止
没有任何场景需要另一个值；没人拧的旋钮就是没人测的旋钮。真出现场景时再变成
`Config` 字段 —— 在那之前不变。

### 4.3 空 root 意味着工作目录

`Root` 为空时 `New` 回落到 `.`，因为 API 测试构建 runtime 时并不给 project root
（`api/v2_test.go:629`）。它仍然是确定的（进程 cwd），也不是一次配置查找：命名一个
root 的文章仍然只有一种写法。

### 4.4 垃圾目录按名字忽略；会话目录不忽略

`.git`、`node_modules`、`$RECYCLE.BIN`、`System Volume Information` 按名字跳过。
`.openheron/state` **不**跳过：上传的文件就在它下面，运行提示词把模型指向那里。
保护上下文的是结果上限，不是忽略。

## 5. 设计

### 5.1 写边界 —— 以及读为什么不再收敛

```go
// agentkit/fsx/fsx.go
func (s *Local) resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return s.root, nil              // never "/"
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, p)    // relative paths are project-relative
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p, nil                       // no containment check: see below
}
```

`resolve` 被所有方法共用，只决定一个请求路径**指的是什么**：空路径是 project root，
相对路径挂在它下面。**读是故意不收敛的。** 工具集里本来就有 `execute`，它能 `cat`
任何进程读得到的文件 —— 所以限制 `read_file` 并没有换来安全，只换来了「项目就在隔壁
目录却莫名 file not found」。原始事故需要的从来不是读禁令，而是**遍历的上限**（§5.3）。

真正的边界是写，它走 `resolveWrite`：

```go
func (s *Local) resolveWrite(p string) (string, error) {
	abs, err := s.resolve(p)
	if err != nil {
		return "", err
	}
	if !s.inside(abs) {
		return "", fmt.Errorf("path %q is outside the project root %q", abs, s.root)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !s.inside(real) {
				return "", fmt.Errorf("path %q resolves outside the project root %q", abs, s.root)
			}
			return abs, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("cannot verify that %q is inside %q: %w", abs, s.root, err)
		}
		if filepath.Dir(dir) == dir {
			return abs, nil
		}
	}
}
```

两个点，是复查才补上的：

- **要向上找祖先，因为文件可能还不存在。** `EvalSymlinks` 解不了不存在的路径，所以
  循环走到最深的已存在目录再检查。没有这一步，当 `link` 指向外部时，`write_file` 写
  `<root>/link/new.txt` 能通过纯字面的检查。
- **★ 它 fail-closed。** 只有「这个组件不存在」才继续向上走。其它任何失败 —— 权限、
  I/O、符号链接成环 —— 都是我们看不进去的目录，于是直接拒绝写。第一版把任何错误都
  当成「这里没有符号链接」，返回未经检查的字面路径，等于一个读不了的祖先就能写到
  任何地方。

`s.roots` 是 project root 加 `ExtraRoots`（会话工作区与 skills 目录 —— 它们可能活在
项目之外）。每个 root 在 `New` 里一次性变成绝对、去符号链接的真实路径，所以比较的是
真实路径。

拒绝是**错误**，不是空结果：模型能拿到一句可以据此行动的话。

### 5.2 `ls` —— 只排一次序、标出目录、封顶

```go
// os.ReadDir sorts by file name already; upstream sorted a second time.
entries, err := os.ReadDir(path)
...
fi := filesystem.FileInfo{Path: entry.Name(), IsDir: entry.IsDir()}
if fi.IsDir {
	// The renderer drops IsDir, so the trailing slash is what tells the model
	// this is a directory it can list and cannot read.
	fi.Path += "/"
}
```

- `Size` 与 `ModifiedAt` 也填上了。现在的渲染层不看它们；代价是每条一次
  `entry.Info()`，而条目最多 200 条。
- 超过 `maxLsEntries` 后，列表以一行合成文本收尾：
  `… N more entries; list a narrower path`。
- 每条检查一次 `ctx.Err()`；上游在这里根本不看 context。

### 5.3 `glob` —— 有界、且扛得住坏目录的遍历

```go
err = filepath.WalkDir(base, func(p string, d os.DirEntry, walkErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return nil // one unreadable subtree skips itself
	}
	...
	if d.IsDir() {
		if s.ignored(d) || strings.Count(rel, "/") >= maxGlobDepth {
			return filepath.SkipDir
		}
		// ★ 按模式剪枝：目录名对不上模式的下一段，它就不可能装着匹配，
		//   走它没有意义。docs/** 只走 docs/，*.go 一个目录都不走。
		if !mayHold(segments, rel) {
			return filepath.SkipDir
		}
		return nil
	}
	if ok, _ := doublestar.Match(pattern, rel); ok {
		matches = append(matches, rel)
		if len(matches) >= maxGlobResults {
			return filepath.SkipAll // stop walking: the rest can only be noise
		}
	}
	return nil
})
```

四条性质，各自回答 §1 的一行：

| 行 | 答案 |
|---|---|
| 缺口 3 | 深度上限、结果上限、忽略列表，以及从一个允许的 root 起步的遍历 |
| 缺口 4 | `walkErr != nil` 返回 nil，遍历跳过这棵子树继续走 |

`SkipAll` 是关键：撞到上限必须**停下**遍历，而不是继续 stat 一百万条再排序 200 条。

★ **遍历还按模式剪枝。** 目录名对不上模式的下一段，它就不可能装着匹配，走它没有
意义：`docs/**` 只走 `docs/`，`*.go` 一个目录都不走。判断是模式的一段对目录在
那个深度上的名字——遍历只会走到祖先已经对上的目录，所以一个深度就够。

★ **遍历还有时间预算。** 上限管的是回来多少，预算管的是这一轮能被堵多久
（§5.6 的 `walkBudget`）。`grep` 拿同一份预算，因为 `rg` 一样要遍历它拿到的那棵树。

### 5.4 绝对 pattern 会被归一化

```go
// The run prompt tells the model to pass "<projectRoot>/**/*.go", while matches are
// relative to the walk root: without this the call silently found nothing.
func (s *Local) globPattern(base, pattern string) (string, error)
```

绝对 pattern 会变成相对遍历根的形式；指向遍历根之外的会被拒绝，并同时报出两个路径
（这是 pattern 与 path 自相矛盾，不是收敛规则）。这修掉了 §1.1，同时让提示词里现有的建议继续可用 —— 于是这件事不需要改任何
提示词文本。

### 5.5 其余五个方法

| 方法 | 改动 | 为什么 |
|---|---|---|
| `Read` | 只用 `resolve` | 读按设计是开放的（§5.1） |
| `GrepRaw` | `resolve`；pattern 前加 `--`；`cmd.Dir = root`；结果走管道读取并封顶；引擎在构造期定一次（§5.8） | `-foo` 是 pattern 不是 flag；`rg` 继承一个确定的 cwd；上游把整份匹配缓冲了两遍 |
| `Write` / `Edit` | `resolveWrite` | 写边界，包括借符号链接出去 |
| `ExecuteStreaming` | `cmd.Dir = root`，保留 `ValidateCommand`；跑命令的程序由调用方给（§5.7） | shell 命令里的相对路径现在意味着 project root |

`GrepRaw` 逐行读 `rg --json`，到 `maxGrepMatches` 就停，并杀掉进程而不是让它把一次
没人再读的扫描跑完。

★ **封顶与引擎提示挂在最后一条匹配的内容上，而不是做成一条匹配。** 渲染层数的是
它拿到的路径与条目：一条自己占位的提示会把「两个文件」变成「三个文件」，而在
`files_with_matches` 模式下（那是默认模式）它只会渲染路径，于是那个被搜索的
目录会混进「命中的文件」里。挂在内容上，计数一个都不多，内容模式下它自己独占
一行。

有 `rg` 的时候，`GrepRaw` 交给 `rg`，它尊重 `.gitignore`；`glob` 不尊重 —— 这个
不对称是上游的，保留，并在相关处用 `shortcut:` 注释写明。没有 `rg` 的时候走
内置引擎（§5.8），它不读 `.gitignore`。

### 5.6 默认值

| 常量 | 值 | 含义 |
|---|---|---|
| `maxLsEntries` | 200 | 一次 `ls` 的结果，外加一行封顶提示 |
| `maxGlobResults` | 200 | 一次 `glob` 的结果，外加一行封顶提示 |
| `maxGlobDepth` | 16 | 相对遍历根的目录深度 |
| `maxGrepMatches` | 200 | 一次 `grep` 的结果，外加一行封顶提示 |
| `walkBudget` | 10s | 一次遍历（glob）或一次搜索（grep）最多跑多久 |
| `ignoredNames` | `.git`、`node_modules`、`$RECYCLE.BIN`、`System Volume Information` | 按 basename 跳过 |

★ **`walkBudget` 管的是开销，不是结果。** 深度与结果上限说「回来多少」，时间预算
说「这一轮能被堵多久」。在挂载盘上跑一个模式，会访问几百万个条目、回来两个
匹配——而工具调用是同步的，没有预算这一轮就会一直挂在盘上。预算耗尽时遍历
停下，并在结果里说明：一个看起来完整、其实被截断的搜索，正是模型得出
「这个文件不存在」的路径。

封顶那两行是 `shortcut:`：ls/glob 工具只渲染 `FileInfo.Path`，所以上限只能表达成
「又多一条条目」。这在对话记录里是诚实的，也不需要改 middleware。

### 5.7 跑命令的程序归谁管

★ **后端不再写死 `/bin/sh -c`。** `Config.Shell` 是程序加它自己的开关
（`["/bin/sh", "-c"]`、`["cmd.exe", "/c"]`），`Config.ShellPathDir` 会被放到子进程
PATH 的最前面 —— 一个在 PATH 之外找到的 shell（Git for Windows、MSYS2）否则找不到
自己那套 `ls`。命令接在开关后面（`agentkit/fsx/fsx.go:775`）。

★ **决定归调用方，因为描述与执行必须是同一个答案。** 探测在 `agentkit/shell`
（`agentkit/shell/shell.go:84` 的 `Detect`），一份快照一次：

1. PATH 上的 `sh` 或 `bash`；
2. Git for Windows 与 MSYS2 的常见安装位；
3. WSL 启动器（`C:\Windows\System32\bash.exe`）—— 排在最后：它把命令跑在 Linux
   文件系统里，那边的 `C:\Users` 是 `/mnt/c/Users`；
4. 一个都没有就 `cmd.exe /c`。

★ **每个候选都要验一下活**（跑一条 `exit 0`）：Windows 自带的那个 `bash.exe` 每台
机器都有，没有 WSL 发行版时它启动、打一行错、退出码非零 —— 把它当成 shell 比没有
shell 更糟。第一个能跑的候选就是它。

★ **`execute` 工具是 openheron 自己的**（`agentkit/build/execute.go`），框架那个不挂
（`agentkit/build/assemble.go:32`）：框架的描述只字不提这台机器上跑命令的是哪个
shell，而写命令的是模型 —— 描述是唯一能让模型知道方言的地方，于是方言写死在描述里
（`agentkit/shell/shell.go:230` 的 `Note`）。名字仍然保留，provider 不得冒用。

### 5.8 ripgrep 缺席时的降级

★ **引擎在构造期定一次。** `fsx.New` 里 `exec.LookPath("rg")` 一次，记在 `Local`
的 `rgPath` 上；空就是内置引擎。不每次调用都试一次 —— 一台没有 ripgrep 的机器
不该为每一次搜索付一次失败的进程，而且第一次调用不该是发现这件事的那一次。

★ **为什么不用系统的 grep。** 这个工具的输出是**数据**（路径、行号、一行文本），
而 grep 的输出是**给人看的文本**。`path:line:content` 是没法可靠地解析回去的：

| 现象 | 实测 |
|---|---|
| 单个文件不给文件名 | `grep -n beta a.txt` → `2:beta` |
| 上下文行换分隔符 | `path-3-gamma` |
| 路径里有冒号 | `C:\...\a.txt:2:beta` —— 盘符自带冒号 |
| 内容里带 `12:` | 内容自己带冒号时，贪婪切与非贪婪切都会切错一边 |
| 方言不同 | GNU（Linux）、BSD（macOS）、busybox（容器）、Git 自带的 GNU 3.0 |

★ 而 ripgrep 的 `--json` 给的是**结构化事件** —— 上游当初读 `--json` 而不是文本
输出，正是为这个。所以降级不是「换个 grep」，是「自己算」。

★ **换不掉框架的 grep 工具。** 文件工具那六个（`ls` `read_file` `write_file`
`edit_file` `glob` `grep`）全部由中间件从 `Backend` 挂上，而 `deep.Config` 只暴露
Backend / Shell / StreamingShell 三个字段（`eino@v0.9.22/adk/prebuilt/deep/deep.go:219`）——
想不要框架的 `grep`，就得连整套文件工具一起不要。所以缝只有一条：`GrepRaw` 返回的
那份 `[]GrepMatch`。

★ **层与层之间只差一件事：谁去找匹配。** 工具本体、渲染、输出模式、分页都在 eino
那边（`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:950`），我们只换掉
「去哪找匹配」这一段。

| 层 | 何时用 | 输出形状 | 谁解析 |
|---|---|---|---|
| `rg --json` | `rg` 在 PATH | 结构化 JSON 事件 | 我们已经在解析 |
| 内置 Go 引擎（RE2） | `rg` 不在 | 我们自己产出的三元组 | 逐字可控 |
| 字面量搜索 | pattern 编不过 | 同上 | 同上 |

★ **pattern 编不过就按字面量搜**：模型要的是一个字符串，给它一个字符串比拒绝它好
—— 但结果里会写明「这个 pattern 不是合法正则，是按字面搜的」。而按字面搜**什么
都没搜到**时，调用直接失败，而不是回答「没有匹配」：一个从来没被照写的意思解释过
的 pattern，「没有匹配」是个没人问过的问题的答案。

★ **内置引擎的行为边界：**

| 维度 | `rg` | 内置引擎 | 差异怎么处置 |
|---|---|---|---|
| 正则方言 | Rust regex（RE2 家族） | Go `regexp`（RE2） | 同族；lookaround 与反向引用两边都不支持 |
| `\d` `\w` | Unicode 感知 | ASCII | 已知差异 |
| 大小写 | `-i` | `(?i)` | 一致 |
| 多行 | `-U --multiline-dotall` | `(?s)` 整文件匹配，行号取起始行 | 一致 |
| 文件过滤 | `--glob` | doublestar（已是直接依赖） | `--glob` 不含 `/` 时按 basename 匹配，同样处理 |
| 类型过滤 | `--type` 内置类型表 | 一张小的扩展名表 | 表外的类型**报错**，不静默搜错文件 |
| 忽略规则 | 尊重 `.gitignore` / `.ignore` | 不认 | 只多搜不少搜 |
| 隐藏文件 | 默认跳过 | 同样跳过 | 一致 |
| 二进制 | 自动跳过 | 前 8 KiB 见 NUL 即跳过 | 一致 |
| 符号链接 | 不跟随 | 不跟随 | 一致 |
| 并行 | 多线程 | 单线程 | 同一份 `walkBudget` 兜底，超时提示语不变 |
| 上限 | 200 条 | 同样 200 条 | 提示语一字不变 |
| 深度 | 没有 | 16 层（和 `glob` 的遍历一样） | **会少搜**：极深的目录里 `rg` 找得到、内置引擎找不到 |

★ **方向大体是安全的**：内置引擎不读 `.gitignore`，所以它**多**搜而不是少搜 ——
于是「这个文件不存在」的结论在两个引擎下都成立。会少搜的只有上面那一条深度上限，
而它跟 `glob` 的遍历是同一个数。

★ **提示挂在最后一条匹配的内容上**（§5.5）：三种输出模式里计数一个都不多；而
`files_with_matches`（默认模式）只渲染路径，模型看不到那句提示 —— 真正会误导
模型的场景（没有结果）走的是错误通道。

★ **落点**：`agentkit/fsx/grep_fallback.go` 是引擎，`agentkit/fsx/fsx.go` 分流，
`agentkit/build/snapshot.go` 在启动日志里打印挑中的是哪个引擎。

★ 将来若按 §8 第 4 条把工具层收回来（自己写那七个工具），那句提示就能进工具描述
里；现在它只能挂在结果上。

## 6. 改动落在哪里

| 文件 | 改动 |
|---|---|
| `agentkit/fsx/fsx.go` | 新增：这个分支，823 行，保留上游 header |
| `agentkit/fsx/fsx_test.go` | 新增：边界、上限、pattern 修复 |
| `agentkit/fsx/grep_fallback.go` | 新增：ripgrep 缺席时用的内置引擎（§5.8） |
| `agentkit/fsx/grep_fallback_test.go` | 新增：内置引擎的用例 |
| `agentkit/fsx/exec_shell_test.go` | 新增：命令真的交给了配置的程序、shell 目录进了子进程 PATH |
| `agentkit/build/snapshot.go:72` | `fsx.New(Config{Root: projectRoot, ExtraRoots: [dataDir, skillsDir], Shell: …, ShellPathDir: …})` |
| `agentkit/build/skill.go:96` | `fsx.New(Config{Root: dir})` —— skill 列表只读一个目录 |
| `agentkit/shell/shell.go` | 新增：探测用哪个 shell，以及给模型读的那句方言 |
| `agentkit/build/execute.go` | 新增：openheron 自己的 `execute` 工具 |
| `go.mod` | 去掉 `eino-ext/adk/backend/local`；`bmatcuk/doublestar/v4` 变成直接依赖 |
| `docs/architecture.md` §2 | 新增 `agentkit/fsx` 的 L1 行 |

别的都不动：工具不动、middleware 不动、审批策略不动、前端不动。

## 7. 测试

`agentkit/fsx/fsx_test.go`：

| 用例 | 断言 |
|---|---|
| `TestGlobStaysInsideRoot` | 与 root 同级、能匹配上的文件不可见 |
| `TestGlobAbsolutePattern` | `<root>/**/*.go` 能匹配 —— §1.1 的回归 |
| `TestGlobAbsolutePatternOutsideRoot` | 越界的 pattern 被拒绝 |
| `TestGlobCapsResults` | 250 个文件 → 200 条 + 一行封顶提示 |
| `TestGlobCancelled` | 取消的 context 能让遍历停下 |
| `TestResolveRejectsEscapes` | `../`、外部绝对路径、`/etc/passwd`、指向外部的符号链接，**写**和**编辑**都被拒绝 |
| `TestReadAllowsOutsideRoot` | 读同级目录是允许的 —— 正是过去会失败的那一幕 |
| `TestResolveWriteFailsClosed` | 权限读不进去的目录后面的写被拒绝，同级的写不受影响 |
| `TestGrepCapsMatches` | 400 条命中 → 200 条，封顶提示挂在最后一条上 |
| `TestTheBuiltinEngineFindsMatches` | 内置引擎找得到行，且结果说明是哪个引擎在搜 |
| `TestTheBuiltinEngineFindsNothing` | 没有结果时**不**多出一条 —— 「没找到」不能变成「找到一个文件」 |
| `TestTheBuiltinEngineIsCaseInsensitive` | `-i` 照旧 |
| `TestTheBuiltinEngineFiltersByGlob` | `--glob` 的 basename 规则照旧 |
| `TestTheBuiltinEngineFiltersByFileType` | `--type` 只看该类型的文件 |
| `TestTheBuiltinEngineRejectsAnUnknownFileType` | 表外的类型报错，不静默搜错文件 |
| `TestTheBuiltinEngineSkipsHiddenFiles` | 隐藏文件跳过（与 `rg` 一致） |
| `TestTheBuiltinEngineSkipsBinaryFiles` | 二进制文件跳过（与 `rg` 一致） |
| `TestTheBuiltinEngineCapsMatches` | 同样的 200 条上限，同样的提示语 |
| `TestTheBuiltinEngineReturnsContextLines` | `-A` / `-B` 的上下文行各自成条 |
| `TestTheBuiltinEngineMatchesAcrossLines` | 多行 pattern 跨行匹配 |
| `TestTheBuiltinEngineSearchesLiterallyWhenThePatternIsNotARegex` | 编不过的 pattern 按字面搜，并说明 |
| `TestTheBuiltinEngineRefusesToReportNoMatchesForAnInvalidPattern` | 编不过又没搜到 → 报错，不回答「没有匹配」 |
| `TestTheBuiltinEngineStopsAtItsBudget` | 同一份预算，提示语不变 |
| `TestGrepFallsBackWhenRipgrepIsMissing` | 没有 ripgrep 的机器上，工具照常工作并说明 |
| `TestEmptyPathMeansRoot` | 空路径列的是 root，不是 `/` |
| `TestLsMarksDirectories` | 目录以 `/` 结尾，文件不是 |
| `TestLsCapsEntries` | 210 条 → 200 条 + 一行封顶提示 |
| `TestExtraRootIsReachable` | extra root 可读，但不是空路径的默认落点 |

手工验证，在那台制造了原始错误的机器上：一次找 `*.md`、路径为 `/mnt/d` 的 `glob`，
现在会走（读是开放的）但带着上限回来 —— 最多 200 条、`$RECYCLE.BIN` 按名字跳过、
读不了的子树跳过 —— 不会卡住。

## 8. 待决问题

1. **提示词仍然写着「永远传绝对路径」。** 现在它只是冗余 —— 绝对路径可用，省略路径
   意味着 project root。把提示词收窄成「相对路径，或省略」是另一件事，有它自己的
   模型行为风险。
2. **`glob` 不认 `.gitignore`。** `rg` 认。如果某项目的构建产物不在垃圾名单里，
   `glob` 就会走进去。~~按 pattern 字面前缀剪枝~~ **已做** —— 遍历现在只走进模式
   可能命中的目录。读 `.gitignore` 本身还没做。
3. **每 agent 的 root。** 整个树共享一个后端实例
   （`agentkit/build/assemble.go:32`）。如果某个子 agent 该比它的父 agent 看得更少，
   就按节点建一个后端 —— 仍然是后端的改动，仍然不用分叉 middleware。
4. **真要改工具层时**（一个 size 列、真正的截断措辞），**不要**复制 middleware：
   把 `deep.Config.Backend` 设为 nil，用 `utils.InferTool` 在 `agentkit/fsx` 之上写
   那七个工具，作为 `cfg.Middlewares` 传进去。约 300 行，不引 `adk/internal`，不
   分叉提示词。
5. **遍历根刻意不设限。** `glob` 与 `grep` 仍然可以被指到项目之外 —— agent 得能去
   模块缓存里查依赖的源码，而 `execute` 本来也能 `cat` 它。真正防止它变成卡死的是
   时间预算。若该把模块缓存加成 root，那是构建层的改动，不是后端的。

## 9. 落地

单个提交，无需迁移：这个包是内部的，磁盘上的会话不动，两个调用点在构建时创建后端。
`task check` 加上 §7 的清单就是全部的闸门。上游同步靠对
`eino-ext/adk/backend/local@v0.2.2` 做手工 diff；`fsx.go` 的文件头写着要对账的版本。
