import { cn } from "@/lib/utils"

/**
 * openheron 标识。
 *
 * ★ 图形来自 `web/public/favicon.svg`（与官网同一套：H 的变形 —— 苍鹭的长喙 +
 *   电路走线与数据点）。**用 <img> 引它而不是内联**：
 *
 *	一份文件两个用途（页签图标 + 界面 logo），改一处就够；内联那份会变成
 *	第三份拷贝，改的时候必然漏掉一处 —— 而漏掉的那处是「logo 与页签图标
 *	长得不一样」，极难发现。
 *
 * ★ alt 留空：logo 旁边永远有 "openheron" 字样，标成图片反而让读屏念两遍。
 */
export function BrandMark({ className }: { className?: string }) {
  return <img src="/favicon.svg" alt="" className={cn("shrink-0", className)} />
}
