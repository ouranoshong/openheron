// ★ The words that mean the same thing wherever they appear.
//
// ★ A shared key is a promise: the word is written **once**, so it is
//   translated once and it is the same word in every place. ⇒ A word that
//   means different things in different places gets its own key under its
//   own surface instead (`sidebar.delete` is not `common.delete`).
export const common = {
  "common.cancel": { en: "Cancel", zh: "取消" },
  "common.close": { en: "Close", zh: "关闭" },
  "common.save": { en: "Save", zh: "保存" },
  "common.loading": { en: "Loading…", zh: "加载中…" },
  "common.rename": { en: "Rename", zh: "重命名" },
  "common.delete": { en: "Delete", zh: "删除" },
  "common.create": { en: "Create", zh: "创建" },
  "common.remove": { en: "Remove", zh: "移除" },
  "common.all": { en: "All", zh: "全部" },
  // ★ The words that two surfaces both need: the `/agents` picker and the
  //   configuration's own section tree say "Agents" for the same thing.
  "common.agents": { en: "Agents", zh: "专家" },
  "common.skills": { en: "Skills", zh: "技能" },
  // ★ The context meter says this when the window is unknown — in the
  //   composer and in the sidebar's agent chip, one sentence.
  "common.contextUsed": { en: "Context: ≈{used} tokens", zh: "上下文：≈{used} token" },
} as const
