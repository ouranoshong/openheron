# 界面动效设计 —— 让界面说「发生了什么」，而不是「它在动」

> ★ **状态：§2.3、§3、§4 已实施；§5 与 §5.1 仍是方案。** 已实施的几节写的是代码
> 里的现状（并指到代码行）；未实施的几节是提案，每条都标了落点与代价。
> 实施之前先读 [frontend-design.md](frontend-design.md)（上游）的 §6（界面布局）、
> §10（验收）与 §10.4（定位点约定）—— 本文与它们一致，不重复也不推翻。

## 0. 一句话

给 openheron 加一层**克制的、纯 CSS 的**动效：动效只负责说明「刚才发生了什么」，
不承载任何信息本身；所有动效都能被系统的「减少动态效果」关掉，关掉之后界面少了一分
生气，但一条信息都没少。

## 1. 现状：现在有多少动效

**落地的动效一共七处**（`web/src/` 全仓扫描）：

| 落点 | 做法 | 位置 |
|---|---|---|
| 对话框浮层与内容 | `data-open:animate-in fade-in-0 zoom-in-95` / `data-closed:animate-out` | `components/ui/dialog.tsx:42`、`:64` |
| 状态点「运行中」 | `bg-info animate-pulse` | `app/shell.tsx:20` |
| 「Working…」提示点 | `bg-info size-1.5 animate-pulse rounded-full` | `components/message-list.tsx:233` |
| 三个折叠箭头 | `transition-transform` + `rotate-90` | `components/tool-card.tsx:89-91`、`components/message-parts.tsx:34`、`components/settings/file-list.tsx:193` |
| 按钮/输入框 | `transition-all` / `transition-colors`，按下 `translate-y-px` | `components/ui/button.tsx:7`、`components/ui/input.tsx:10`、`components/ui/textarea.tsx:9` |
| 加载骨架、三个 spinner | `animate-pulse` / `animate-spin` | `components/ui/skeleton.tsx:7`、`components/composer-meta.tsx:32`、`components/mcp-panel.tsx:90` |
| ★ 品牌标记入场（§4） | `opacity` 淡入 + `stroke-dashoffset` 接通 + 数据点亮起 | `src/index.css`（`mark-parts-in` / `mark-trace-in` / `mark-dot-in`） |

**依赖是现成的**：`tw-animate-css@^1.4.0` 已经引进来（`web/src/index.css:2`），
`animate-in` / `animate-out` / `fade-in-0` / `zoom-in-95` 这些工具类都在用。
★ `tw-shimmer@^0.4.13` 曾经装了但**全仓零引用** —— 它在 2026-11-09 被删掉了
（`web/package.json` 与 `web/bun.lock`），**它是弹药，但不是本方案需要的弹药**：
本方案要的是「淡入 + 接通」两件小事，`tw-shimmer` 一件都不做。

★ **「尊重减少动态效果」原来也是缺的**：动手之前全仓 0 处
`prefers-reduced-motion` —— 上面那六处动效对动效敏感的用户照跑不误，一个
`animate-pulse` 的状态点，对前庭功能障碍者就是一处持续晃动。这不是风格问题，
是缺口。★ **现在补上了**（§2.3），而且补的是全局规则：连 `tw-animate-css` 自带的
那几处也被一起降级。


## 2. 三条铁律

### 2.1 默认态即最终态

**动效只是「到达」的过程，信息在动画结束后必须完整可读、可读屏。**
去掉全部动效，界面上不能少任何一条信息、不能少任何一个可点的东西。

这条决定了后面所有取舍：凡是「靠动效才能看懂」的提案，一律不做。

### 2.2 ★ 新增动效不改变元素的最终布局

只动 `transform` / `opacity` / `filter` 这类**合成器属性**，不动 `height` /
`margin` / `top` / `width`。

**为什么**：消息流靠 `ResizeObserver` 按**内容高度**跟随滚动
（`components/message-list.tsx:105-120`）。动布局会跟这套跟随机制抢方向盘 ——
`scroll.spec.ts` 那 8 条（溢出时停在视口内 / 用户上翻后不被拽回 / 流式未结束也跟随 /
高亮撑高后仍跟随 …）正是盯着这件事的。

**唯一的例外**：折叠面板的展开/收起。它本来就要改布局，动效只是让这次**已经要发生**
的布局变化变平滑 —— 不存在「新增的布局抖动」。

### 2.3 尊重 `prefers-reduced-motion` ★ 已实施

一条全局规则，把所有动效降到「立刻完成」：

```css
@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}
```

`0.01ms` 而不是 `0s`：有 `animation-fill-mode: both` 的入场动画在 `0s` 下会**跳过**
动画直接落到终态，而某些浏览器在 `0s` + `fill-mode: both` 的组合下会跳过关键帧，
导致元素停在起始态。`0.01ms` 保证终态一定被画出来。

★ **落点**：`web/src/index.css`（`@media (prefers-reduced-motion: reduce)` 一段）。
★ 因为是全局规则，它连 `tw-animate-css` 自带的那几处也一起降级了 —— 不只是本文
写的那几处。

## 3. 动效令牌：一份，不是六处 ★ 已实施

`web/src/index.css` 的 `:root` 里加一组，和 `--chat-column` 同一个思路 ——
**改一次，不是改六处**：

```css
:root {
  --motion-duration-fast: 120ms;   /* 悬停、按下、颜色过渡 */
  --motion-duration-base: 200ms;   /* 展开/收起、入场 */
  --motion-duration-slow: 320ms;   /* 品牌标记的一次性入场 */
  --motion-ease-out: cubic-bezier(0.22, 1, 0.36, 1);
  --motion-ease-in-out: cubic-bezier(0.65, 0, 0.35, 1);
}
```

★ **为什么不给 `.dark` 单独一套**：动效时长与缓动跟明暗无关。明暗只影响**颜色**，
而颜色已经有 `--info` / `--warning` / `--success` 那套 token 了（`index.css:56-63`）。
★ **一个颜色也不许写字面量**：动效里出现的颜色一律走 token —— 这是 AGENTS.md 的
「A color is a token, never a literal」。

## 4. 品牌标记：从 `<img>` 到内联 SVG ★ 已实施

### 4.1 为什么必须内联

动手之前，`components/brand-mark.tsx:4` 是：

```tsx
return <img src="/favicon.svg" alt="" className={cn("shrink-0", className)} />
```

★ **装在 `<img>` 里的 SVG 是一个独立的文档**，宿主页面的 CSS 选不进去它的任何内部
元素 —— 这是浏览器的规定，不是实现细节。所以 `<img>` 形态下，喙、颈、电路走线、
数据点**一个都动不了**，能动的只有整个 `<img>` 本身。

而 `web/public/favicon.svg` 的构造恰好是「四个可动的部件 + 一条电路走线 + 一个数据点」：
喙 `M12 11.5 L43 8 L12 17 Z`、颈 `M12.5 16 …`、右干 `rect x=28 y=13`、
横杠 `rect x=12.5 y=24`，加上 mask 之外的走线 `M34.6 27.3 L40 27.3 L40 17.5`
与数据点圆 `(42.6, 17.5)`。

★ **现在的形态**：`brand-mark.tsx` 是一个薄封装，真正的图形在
`brand-mark.generated.tsx` 里 —— 那是**从 `public/favicon.svg` 生成的**（§10.1）。

### 4.2 ★ 结构上的一个坑：主体与描边是两个 mask

`favicon.svg` 用两个 mask 叠出「深色描边 + 渐变主体」：`openheronBody` 挖主体，
`openheronHalo`（放大 1.365 倍）挖描边。★ **两个 mask 里是同一批路径的两份拷贝** ——
动一份不动另一份，描边就会和主体错开。

★ **曾经的解法是错的**：把四个路径收进一个 `<g id="mark-parts">`，两个 mask 各用
`<use href="#mark-parts"/>` 引用它。**`<use>` 是影子树（shadow tree）**：宿主页面的
CSS 选择器**选不进去**影子树里的元素 —— 能穿过去的只有继承属性（颜色可以，动画
不行）。所以 `.mark-beak` 这类 class 挂在 `<use>` 里面是**不会生效**的，部件一个
也动不了。这条是动手时才发现的，写在这里是为了不让下一个人再走一遍。

**现在的解法**：两份拷贝，**同名 class**，动画施加在 class 上。

- `web/public/favicon.svg`：两个 mask 里的 `<g class="mark-parts">` 各带四个部件
  （`mark-beak` / `mark-neck` / `mark-stem` / `mark-bar`）；body 的那一份还带一个
  `mark-eye`（喙上的眼睛洞 —— 它是主体的一部分，所以跟着主体一起淡入，否则会留下
  一个孤零零的黑点）。
- 一条 class 同时命中两份拷贝 → **主体与描边永远同步**，不需要任何「引用」机制。

```svg
<mask id="openheronBody" maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
  <g transform="translate(24 24) scale(1.3) translate(-28.2 -24.5)">
    <g class="mark-parts" fill="#fff">
      <path class="mark-beak" d="M12 11.5 L43 8 L12 17 Z" />
      <path class="mark-neck" d="M12.5 16 L19 16.4 …" />
      <rect class="mark-stem" x="28" y="13" width="6.6" height="28" rx="1.4" />
      <rect class="mark-bar" x="12.5" y="24" width="15.5" height="6.6" rx="1.4" />
      <circle class="mark-eye" cx="17.6" cy="13.4" r="1.55" fill="#000" />
    </g>
  </g>
</mask>
```

★ **mask 里的部件只能做 opacity 动画**：body 的缩放是 1.3、halo 是 1.365，同样的
位移在两份拷贝里走的距离不同 → 描边和主体立刻错开。所以 `.mark-parts` 只淡入，
**不做位移**。

★ 走线与数据点本来就在 mask 之外（`favicon.svg` 的描边 `<g stroke=…>` 与两个圆），
不受这个限制：走线走 `stroke-dashoffset`，数据点走 `opacity` + `scale`。

### 4.3 动效：一次「接通」，然后静止

克制基调下的品牌动作只做一件事 —— **电路接通、数据点亮起**，全程一次性，不循环：

| 部件 | 动作 | 时序 |
|---|---|---|
| `.mark-trace`（电路走线） | `stroke-dashoffset` 从全长到 0 —— 电流接通 | 0ms 起，`--motion-duration-slow` |
| `.mark-dot`（数据点） | 淡入 + 一次轻微放大回弹（`scale(0.6)→1`） | 走线结束后 80ms |
| `.mark-parts`（主体四件） | 整体淡入，**无位移** | 0ms 起，与走线并行 |

★ **主体不做位移**：位移会被读成「生长」，而这个标记是静态的品牌符号，不该有生长感。
★ **只播一次**：用 `animation` 而不是 `transition`，且不写 `infinite`。

★ **落点**：`web/src/index.css`（三个 `@keyframes` + 三条 `.brand-mark--play …` 规则）。

★ **两个实现上的细节**：

- 走线的虚线长度用 `pathLength="1"` 归一化了 —— 样式表因此不必知道这条路径有多长，
  `stroke-dasharray: 1` 就是「整条」。
- 数据点的放大用的是 CSS 的 **`scale` 属性**，不是 `transform`：数据点自带一个
  `transform` 属性（它要落在画布的固定位置），而 CSS 的 `transform` 会**整个盖掉**
  属性值；`scale` 是独立属性，它排在 `transform` 之前施加，两者共存。
  ★ 圆点要绕**自己的圆心**长，所以配了 `transform-box: fill-box` ——
  SVG 的 `transform-origin` 默认取的是**视口**的中心，不是图元的中心。


### 4.4 用在哪儿：只在欢迎页 ★ 已实施

| 落点 | 尺寸 | 提案 |
|---|---|---|
| 欢迎页 hero（`components/message-list.tsx:214`） | `size-16`（64px） | **播一次入场** —— 部件够大，看得清 |
| 页头（`app/shell.tsx:80`） | `size-5`（20px） | **不播** |

★ 页头为什么不播：20px 下四个部件各只有几个像素，细节全糊；而页头是**常驻**的 ——
一个一直动的东西在常驻位置就是噪声，不是信息。

★ 落点：欢迎页那处传 `play`（`brand-mark.tsx` 的 `play` 属性 → `brand-mark--play`
这个 class），页头那处不传。

### 4.5 无障碍

- 内联 SVG 要 `role="img"` + `<title>openheron</title>`（原 `favicon.svg` 已有）；
- 页头那处继续按现状渲染成装饰件（旁边的文字已经写了 openheron），用
  `aria-hidden="true"` 而不是塞一个空的 `alt`；
- ★ `web/public/favicon.svg` 现在是**内联版的源**（§10.1），不是一份「另抄的」东西 ——
  浏览器标签页图标继续用它，不受内联版影响；它多出来的 `class` 与 `pathLength`
  **不改变渲染**（favicon 没有样式表，`pathLength` 只影响虚线计算）。

## 5. 界面功能性动效

原则：**动效 = 状态变化的回声**。每一条都要能回答「界面刚才变了什么」。

| 落点 | 现在 | 提案 | 属性 | 依据 |
|---|---|---|---|---|
| 消息入场 | 无 | 淡入 + 上移 4px | `opacity` / `transform` | 新元素出现；§2.2 合规（不改布局） |
| 审批卡出现 | 无 | 从下 8px 淡入 | `opacity` / `transform` | ★ 它**取代 composer**（`app/shell.tsx:112`），出现得晚、位置显眼 |
| 状态点切换 | 只有 `animate-pulse` | 保留 pulse，另加**颜色过渡**（idle→running 用 `--info`） | `background-color` | 状态点语义见上游 §6.3：`awaiting-approval` 优先于 `running` |
| 「Working…」 | `animate-pulse` 小点 | 保留 | — | 已有 |
| 工具卡展开 | 箭头 `rotate-90` | 箭头保留；**内容**用 `grid-template-rows: 0fr → 1fr` 做高度过渡 | `height`（§2.2 的例外） | 内容本来就要变高，动效只是让它平滑 |
| 折叠箭头 | 已有 `transition-transform` | 补上 `--motion-duration-fast`，替换写死的默认值 | `transform` | 三个地方各写一遍 = 会漂 |
| 对话框 | `data-open`/`data-closed` 已有 | 保留 | — | 现成的模式，新动效照它写 |

★ **审批卡那条是本节唯一「有存在感」的动效**，理由是它值得：审批发生时轮次已经结束、
输入框被取代（上游 §6），用户此刻最需要知道「轮子停在我这儿了」。

### 5.1 ★ 流式光标：新增，但只能做成 CSS 伪元素

现在流式输出**没有光标**，文本一段段蹦出来。

**实现约束是硬的**：★ 消息流**每帧重渲染**（`lib/api.ts:455-463` 的 `FRAME_MS` 批处理，
每个 frame 重新解析 markdown），所以光标**不能是 React 元素** —— 那样每帧都要重挂一次
DOM，成本和闪烁都不可接受。

**只能做成 CSS 伪元素**（`::after` 画一根竖线，`opacity` 呼吸，不占 DOM、不引发重渲染）：

```css
.message-text[data-streaming="true"]::after {
  content: "";
  display: inline-block;
  width: 2px;
  height: 1em;
  margin-left: 2px;
  vertical-align: text-bottom;
  background: var(--color-foreground);
  animation: caret-blink 1s steps(2, start) infinite;
}
```

★ 呼吸用 `opacity` 而不是 `width` / `height`：后者每帧触发布局，在每帧重渲染的列表里
就是一场灾难。

## 6. 性能

- **只动合成器属性**（§2.2）→ 动画跑在合成器线程，不触发布局。
- ★ **入场动效必须是「每条消息一次」，不能是「每 token 一次」**。消息流每帧重渲染，
  按 token 播入场会变成一场持续闪烁 —— 而且 `animate-in` 这类工具类是挂在元素上的，
  元素重渲染会不会重播，取决于 React 是否复用同一个 DOM 节点（会复用）—— **这条必须
  在 E2E 里钉住**，不能靠假设。
- 不写 `will-change`：现代浏览器对 `transform` / `opacity` 的动画会自动提升合成层，
  常驻的 `will-change` 反而占着合成层不放。

## 7. 验收

★ AGENTS.md：「Every interface change needs a Playwright spec」。

**动效本身很难用 Playwright 断言**（时序脆弱，等动画结束就要 sleep，慢了会飘）——
所以断言的是**终态**，不是过程：

| spec | 钉的是什么 |
|---|---|
| 消息发出后消息**在屏幕上**（最终态可见，不是「等它淡入」） | 入场动效没有把元素留在起始态 |
| 审批卡出现后按钮**可点** | 入场动画没有挡住交互 |
| 工具卡展开后内容**在** | 高度过渡没有把内容留在 0fr |
| 用户上翻后新消息到达，**仍在视口内不被拽回** | 动效没有破坏滚动跟随（§2.2） |
| `prefers-reduced-motion: reduce` 下，消息**立刻可见**（不是等 200ms 淡入） | 降级规则真的生效 |

★ **定位点用语义定位**（role / aria / 可见文本），不用 CSS 类名 —— 上游 §10.4：
类名会被 Tailwind 改、被 shadcn 重新生成；语义标签才是产品契约。需要稳定钩子时加
`data-testid`，不要用 `data-slot="aui_*"` 那种框架命名的属性。
★ **例外**：品牌标记的部件 class（`mark-parts` 等）是**手写的语义 class**，写在
`public/favicon.svg` 里，不是 Tailwind 工具类 —— 它跟 `data-testid` 一样稳。

★ **落点**：`web/test/e2e/chat.spec.ts`（消息入场、审批卡）、`web/test/e2e/scroll.spec.ts`
（动效不破坏跟随）、`web/test/e2e/motion.spec.ts`（降级规则）。
★ **上游 `frontend-design.md` §10.3 把 spec 文件数与条数写死在表里** —— 加了用例，
上游那份表也得跟着改（本仓库改不了，得记一笔交给上游）。

### 7.1 ★ §4 这一步的验收（已实施）

`web/test/e2e/motion.spec.ts` 四条，钉的都是**终态**：

| 用例 | 钉的是什么 |
|---|---|
| 欢迎页的品牌标记是**内联 SVG**、在屏幕上、64px | 它不再是 `<img>`（`<img>` 里的 SVG 动不了） |
| 部件的 `opacity` 终值是 1 | 入场没有把部件留在起始态 |
| `.brand-mark--play` 全页只有一处 | 页头那一份不播（§4.4） |
| 降级下 `animation-duration` < 0.01s、部件不透明度已是 1 | §2.3 的全局规则真的生效 |

★ 还有一条**不在浏览器里**的验收：`web/test/brand-mark.test.ts`（`bun run test:unit`）
—— 重新生成一遍，和仓库里那份逐字节比对（漂移检查），外加图形与 class 的往返比对。
生成物与源不一致时，它**先于界面**发现问题。

## 8. 落地顺序

1. **地基**：动效令牌（§3）+ `prefers-reduced-motion` 全局规则（§2.3）。
   ★ 先做这个，因为它**立刻止损**：动手之前所有动效对动效敏感用户都是无防护的。
   ★ **已完成**。
2. 品牌标记内联 + hero 入场（§4）—— 一次性的、可见的、最好验收的一步。
   ★ **已完成**。
3. 消息入场 + 审批卡入场 + 状态点颜色过渡（§5）。
4. 工具卡展开的高度过渡（§5 的例外项）。
5. 流式光标（§5.1）—— 单独一步，因为它要动 `message-parts.tsx` 的渲染路径。

每一步都自带 E2E；**分步落地**是因为第一步就能独立成立，后面任何一步做坏了都能退回去。

## 9. 明确不做

- **不引入动画库**（framer-motion / motion 等）：`tw-animate-css` 已经够用；而前端是
  嵌进单个二进制的（`go:embed`），多一个依赖就多一份分发体积。
- **不做滚动联动**（`animation-timeline: view()`）：那是网站项目的叙事手法
  （内容随滚动进场）；openheron 是**应用界面**，没有「滚动即叙事」这回事，而且它要的是
  §2.2 说的「不改布局」。
- **不做氛围型动效**（背景光晕、粒子、渐变流动）：基调已定 —— 克制。
- **不循环播放任何东西**：唯一的循环是流式光标与已有的 `animate-pulse`，它们都是
  「状态还挂着」的诚实表达。

## 10. ★ 落地时必须处理的两个尾巴

### 10.1 favicon.svg 与内联版：一份源，多个生成物 ★ 已处理

`web/public/favicon.svg`（浏览器图标）与界面里的内联版，**必须是同一张图**。

- ★ 两份手工维护的拷贝会**漂**：改了图形忘了改另一份，浏览器标签页和界面里的标记
  就不再是同一个东西。
- ★ **处理办法不是「接受两份」，而是让它变成一份源、多个生成物**：
  - `web/public/favicon.svg` 是**唯一的源**；
  - `web/scripts/generate-brand-mark.ts` 把它生成成
    `web/src/components/brand-mark.generated.tsx`（内联组件）；
  - `web/package.json` 里 `bun run gen:brand` 重新生成，`bun run gen:brand:check`
    检查有没有漂；`build` 与 `dev` 都先跑一遍生成。
- ★ **漂移检查在单测里**（`web/test/brand-mark.test.ts`）：重新生成一遍，和仓库里
  那份逐字节比对。CI 跑 `bun run test:unit`，所以**生成物过时会在 CI 上失败**，
  而不是等到某个人的浏览器里发现标签页和界面对不上。
- ★ 生成物是**提交进仓库**的：前端最后是 `go:embed` 进单二进制的，构建机上不该
  依赖一个「先跑个脚本」的步骤。

### 10.2 `tw-shimmer` 是个悬着的依赖 ★ 已处理

装了、没被引用。★ **删依赖要单独一个提交** —— 它和动效方案无关，混在一起会让
这次改动的边界变模糊。

★ **结果：删掉了**（`web/package.json` 与 `web/bun.lock`）。本方案没有需要它的
地方 —— 它要的是「淡入 + 接通」两件小事，`tw-shimmer` 一件都不做。

## 11. 后续（不在本轮）

- §5 的几条界面功能性动效（消息入场、审批卡入场、状态点颜色过渡、工具卡展开）。
- §5.1 的流式光标。
- 上游 `frontend-design.md` §10.3 的条数表同步（§7）。
- 折叠面板的高度过渡若在实测中与滚动跟随有摩擦，退回「直接切换 + 箭头旋转」。
