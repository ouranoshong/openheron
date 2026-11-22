// ★ The interface's copy, in **both languages, side by side**.
//
// ★ Each entry carries its two languages together: a translator reads the
//   English and writes the Chinese on the next line, and the two cannot
//   drift apart because there is only one place they are written.
//
// ★ The key set is a **type**. `MessageKey` is the union of the keys, so a
//   key that does not exist is a compile error **where it is used** — the
//   same discipline the bilingual documents have
//   (`scripts/check-doc-pairs.py`), applied to the code.
//
// ★ `zh: ""` means "not written yet": the interface falls back to English
//   (see `lib/i18n.ts`). A rough edge is better than an empty space.

import { common } from "./parts/common"
import { header } from "./parts/header"
import { lib } from "./parts/lib"
import { sidebar } from "./parts/sidebar"
import { projects } from "./parts/projects"
import { composer } from "./parts/composer"
import { messages } from "./parts/messages"
import { settings } from "./parts/settings"

/** Every string the interface can show. */
export const catalog = {
  ...common,
  ...header,
  ...lib,
  ...sidebar,
  ...projects,
  ...composer,
  ...messages,
  ...settings,
}

/** The key of one string. */
export type MessageKey = keyof typeof catalog

/** One string, in the two languages the interface speaks. */
export type Entry = { readonly en: string; readonly zh: string }
