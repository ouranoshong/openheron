// ★ The copy of the **projects**: the two group headings the sidebar draws,
//   and the sentences the form refuses a name or a root with.
//
// ★ The headings live here and not in `lib/projects.ts` because they are
//   copy: the English is what the interface shows today, byte for byte, and
//   the Chinese goes on the next line when it is written.
export const projects = {
  // the two headings the sidebar draws above a group of conversations
  "projects.group.pinned": { en: "Pinned", zh: "已置顶" },
  // ★ The default project is named **once**: the sidebar's group heading, the
  //   button under the input box and the project's own row all say the same
  //   word for the same place, and one key is what keeps them one word.
  "projects.defaultName": { en: "Default Project", zh: "默认项目" },
  // ★ The action on a project's row — in the sidebar's menu and in the
  //   project's own dialog, it is the same action and the same two words.
  "projects.changeFolder": { en: "Change folder", zh: "更改文件夹" },

  // why the form refuses a name or a root
  "projects.error.nameOneLine": { en: "The name must be one line.", zh: "名称必须是单行。" },
  "projects.error.nameNotEmpty": { en: "The name must not be empty.", zh: "名称不能为空。" },
  "projects.error.rootNotEmpty": { en: "The root must not be empty.", zh: "根目录不能为空。" },

  // the folder chooser answered, but not with a folder
  "projects.pick.noDirectory": { en: "The server did not say which directory.", zh: "服务端没有指明目录。" },
} as const
