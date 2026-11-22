import { describe, expect, test } from "bun:test"

import { translator } from "../src/lib/i18n"

import {
  NO_PROJECT_GROUP,
  PINNED_GROUP,
  directoryPick,
  foldGroups,
  groupSessions,
  projectNameError,
  projectRenameError,
  projectRootError,
  projectSwitch,
  resolveActiveProject,
  sortProjects,
  type ProjectScope,
  type ProjectSummary,
} from "../src/lib/projects"

function project(id: string, order: number, name = id): ProjectSummary {
  return { id, name, root: `/tmp/${id}`, order, sessionCount: 0 }
}

function session(threadId: string, projectId?: string, pinned = false) {
  return { meta: { threadId, projectId, pinned } }
}

const scope = (id: string): ProjectScope => ({ projectId: id, name: id, root: `/tmp/${id}` })

// ★ The English translator: the catalog's English is the text the interface
//   showed before the copy moved into it.
const en = translator("en")

describe("groupSessions —— 多项目模式", () => {
  test("置顶组在最前，项目按 order，未选项目在最后", () => {
    const groups = groupSessions(
      [session("a", "beta"), session("b", "alpha"), session("c"), session("d", "beta", true)],
      [project("beta", 2), project("alpha", 1)],
      null,

    en,

    )
    expect(groups.map((g) => g.key)).toEqual([PINNED_GROUP, "alpha", "beta", NO_PROJECT_GROUP])
    expect(groups[0]!.sessions.map((s) => s.meta.threadId)).toEqual(["d"])
    expect(groups[1]!.sessions.map((s) => s.meta.threadId)).toEqual(["b"])
    expect(groups[2]!.sessions.map((s) => s.meta.threadId)).toEqual(["a"])
    expect(groups[3]!.sessions.map((s) => s.meta.threadId)).toEqual(["c"])
  })

  test("组标题是项目的名字", () => {
    const groups = groupSessions([session("a", "alpha")], [project("alpha", 0, "CodeLab")], null, en)
    expect(groups[0]!.title).toBe("CodeLab")
  })

  // ★ An empty **project** is a row in the map: ★ the sidebar's actions on a
  //   project live on the project's own row, so a project that is not drawn
  //   is a project that cannot be renamed, moved or removed.
  test("空项目也画出来，它是地图上的一格", () => {
    const groups = groupSessions([session("a", "alpha")], [project("alpha", 0), project("empty", 1)], null, en)
    expect(groups.map((g) => g.key)).toEqual(["alpha", "empty"])
    expect(groups[1]!.sessions).toEqual([])
  })

  test("一个会话只在一个组里", () => {
    const groups = groupSessions([session("a", "alpha", true)], [project("alpha", 0)], null, en)
    // ★ The pinned conversation is in the pinned group and nowhere else — and
    //   the project it came from is still a row, holding nothing.
    expect(groups[0]!.key).toBe(PINNED_GROUP)
    expect(groups[0]!.sessions.map((s) => s.meta.threadId)).toEqual(["a"])
    expect(groups[1]!.key).toBe("alpha")
    expect(groups[1]!.sessions).toEqual([])
  })

  test("表里没有的项目 id 落到未选项目组", () => {
    const groups = groupSessions([session("a", "gone")], [project("alpha", 0)], null, en)
    // ★ The conversation whose project is gone lands in the "no project"
    //   bucket, and the project that is still in the table is still a row.
    expect(groups.map((g) => g.key)).toEqual(["alpha", NO_PROJECT_GROUP])
    expect(groups[1]!.sessions.map((s) => s.meta.threadId)).toEqual(["a"])
  })

  test("★ 两个特殊组的标题来自目录，不是写死的字面量", () => {
    // ★ The heading is **copy**: it is said in the reader's language, so it
    //   comes from the catalog and not from a constant in this file.
    const groups = groupSessions(
      [session("a", "alpha"), session("b", "alpha", true), session("c")],
      [project("alpha", 0)],
      null,
      en,
    )
    expect(groups[0]!.title).toBe("Pinned")
    expect(groups[2]!.title).toBe("Default Project")
  })

  test("没有项目也没有会话时没有组", () => {
    expect(groupSessions([], [], null, en)).toEqual([])
  })
})

describe("groupSessions —— 单项目模式", () => {
  test("只有一组：作用域项目 + 未选项目的会话", () => {
    const groups = groupSessions(
      [session("a", "alpha"), session("b"), session("c", "other")],
      [project("alpha", 0), project("other", 1)],
      scope("alpha"),

    en,

    )
    expect(groups).toHaveLength(1)
    expect(groups[0]!.key).toBe("alpha")
    expect(groups[0]!.title).toBe("alpha")
    expect(groups[0]!.sessions.map((s) => s.meta.threadId)).toEqual(["a", "b"])
  })

  test("别的项目的会话不进来", () => {
    const groups = groupSessions([session("c", "other")], [project("other", 0)], scope("alpha"), en)
    expect(groups).toEqual([])
  })

  test("★ 作用域项目在表里时，没有会话也画出来", () => {
    // ★ The scope project is the one directory this process serves, and the
    //   map says so even when nothing has been started in it yet.
    const groups = groupSessions([], [project("alpha", 0)], scope("alpha"), en)
    expect(groups).toHaveLength(1)
    expect(groups[0]!.key).toBe("alpha")
    expect(groups[0]!.sessions).toEqual([])
  })

  test("只有未选项目的会话时也在那一组里", () => {
    const groups = groupSessions([session("b")], [], scope("alpha"), en)
    expect(groups).toHaveLength(1)
    expect(groups[0]!.title).toBe("alpha")
  })
})

describe("sortProjects", () => {
  test("order 在前，id 破平局", () => {
    const out = sortProjects([project("b", 1), project("a", 1), project("c", 0)])
    expect(out.map((p) => p.id)).toEqual(["c", "a", "b"])
  })

  test("不改原数组", () => {
    const input = [project("b", 1), project("a", 0)]
    sortProjects(input)
    expect(input.map((p) => p.id)).toEqual(["b", "a"])
  })
})

describe("resolveActiveProject", () => {
  test("单项目模式由作用域说了算", () => {
    expect(resolveActiveProject("alpha", [project("alpha", 0), project("beta", 1)], scope("beta"))).toBe("beta")
  })

  test("多项目模式保留用户选的项目", () => {
    expect(resolveActiveProject("beta", [project("alpha", 0), project("beta", 1)], null)).toBe("beta")
  })

  test("选的项目没了 —— 退回「没有项目」，不是列表里的第一个", () => {
    // ★ A project the table no longer has must not stay selected, and the
    //   first project is not a stand-in for it: that one is a directory the
    //   user did not pick for this conversation.
    expect(resolveActiveProject("gone", [project("alpha", 0)], null)).toBe("")
  })

  test("一个项目都没有时也是「没有项目」—— 那是默认项目根", () => {
    expect(resolveActiveProject(null, [], null)).toBe("")
  })

  test("「Default Project」是一个选择 —— 刷新不能把它改掉", () => {
    // ★ The sidebar offers "Default Project", and the server accepts a
    //   conversation without a project — it works in the default project root.
    //   ★ A refresh must not turn that choice back into the first project.
    expect(resolveActiveProject("", [project("alpha", 0), project("beta", 1)], null)).toBe("")
  })

  test("还没选过（null）时兜底是「Default Project」—— 也就是默认项目根", () => {
    expect(resolveActiveProject(null, [project("alpha", 0), project("beta", 1)], null)).toBe("")
  })
})

describe("projectSwitch", () => {
  test("屏幕上没有会话时，切换只是选一个项目", () => {
    expect(projectSwitch("beta", undefined)).toEqual({ projectId: "beta", leaveSession: false })
  })

  test("会话已经在这个项目里 —— 不动它", () => {
    expect(projectSwitch("beta", "beta")).toEqual({ projectId: "beta", leaveSession: false })
  })

  test("会话在别的项目里 —— 换项目就得离开它", () => {
    // ★ A conversation's project is immutable: there is no path that moves
    //   one, so the composer comes back empty and the next conversation is
    //   created in the project that was picked.
    expect(projectSwitch("beta", "alpha")).toEqual({ projectId: "beta", leaveSession: true })
  })

  test("没有项目的会话也是一个地方 —— 换个项目同样得离开", () => {
    expect(projectSwitch("beta", "")).toEqual({ projectId: "beta", leaveSession: true })
  })
})

describe("projectNameError", () => {
  test("空名字不算错 —— 新建时它可以由目录名推出来", () => {
    // ★ The name is optional: the server derives it from the directory.
    //   So the empty name must not stop the create.
    expect(projectNameError("", en)).toBeUndefined()
    expect(projectNameError("   ", en)).toBeUndefined()
  })

  test("名字只能是一行", () => {
    // ★ The sidebar draws the name as a heading, and a name with a newline
    //   in it breaks that heading into two.
    expect(projectNameError("api\ngateway", en)).toBeDefined()
    expect(projectNameError("api\r\ngateway", en)).toBeDefined()
  })

  test("普通名字没有理由", () => {
    expect(projectNameError("CodeLab", en)).toBeUndefined()
  })
})

describe("projectRenameError", () => {
  test("重命名时名字是必填的 —— 项目本来就有名字", () => {
    // ★ Renaming is the one place where the name is required: the server
    //   refuses an empty one ("the project name must not be empty").
    expect(projectRenameError("", en)).toBeDefined()
    expect(projectRenameError("  ", en)).toBeDefined()
  })

  test("重命名也是一行", () => {
    expect(projectRenameError("api\ngateway", en)).toBeDefined()
  })

  test("普通名字没有理由", () => {
    expect(projectRenameError("CodeLab", en)).toBeUndefined()
  })
})

describe("foldGroups", () => {
  const groups = groupSessions(
    [session("a", "alpha"), session("b", "alpha"), session("c", "beta")],
    [project("alpha", 1), project("beta", 2)],
    null,

  en,

  )

  test("没折叠的组原样", () => {
    const folded = foldGroups(groups, new Set())
    expect(folded.map((g) => g.folded)).toEqual([false, false])
    expect(folded.map((g) => g.sessions.length)).toEqual([2, 1])
  })

  test("★ 折叠的组仍然有标题，也有它的会话", () => {
    // ★ The fold is a **display** state: the group is still there, and the
    //   count of a folded group is still its count — a group that says
    //   nothing about how much is in it is a group the user has to open to
    //   know.
    const folded = foldGroups(groups, new Set(["alpha"]))
    expect(folded[0]!.folded).toBe(true)
    expect(folded[0]!.title).toBe("alpha")
    expect(folded[0]!.sessions.length).toBe(2)
    expect(folded[1]!.folded).toBe(false)
  })

  test("折叠的是组键，不是项目 id 碰巧相同的东西", () => {
    // ★ A key that names no group simply never matches.
    const folded = foldGroups(groups, new Set(["gone"]))
    expect(folded.map((g) => g.folded)).toEqual([false, false])
  })

  test("★ 空组不折叠 —— 没有东西可以收起来", () => {
    // ★ A folded heading that says "0" is a folder the user has to open to
    //   learn that it is empty.
    const withEmpty = groupSessions([session("a", "alpha")], [project("alpha", 0), project("empty", 1)], null, en)
    const folded = foldGroups(withEmpty, new Set(["empty"]))
    expect(folded.map((g) => g.folded)).toEqual([false, false])
  })
})

describe("projectRootError", () => {
  test("目录是唯一必填项，空的不行", () => {
    expect(projectRootError("", en)).toBeDefined()
    expect(projectRootError("  ", en)).toBeDefined()
  })

  test("目录不检查存不存在 —— 那是服务端的事", () => {
    // ★ The client must not keep a second copy of the rules: the server
    //   resolves the path, and it is the one that says no.
    expect(projectRootError("/tmp/does-not-exist", en)).toBeUndefined()
  })
})

describe("directoryPick", () => {
  test("选中了就是那条路径", () => {
    const got = directoryPick({ picked: true, path: "/tmp/picked" }, en)
    expect(got).toEqual({ kind: "picked", path: "/tmp/picked" })
  })

  test("关掉选择器不是失败", () => {
    // ★ "Not now" is how a person says it, and the client must not report
    //   it as an error.
    const got = directoryPick({ picked: false, path: "" }, en)
    expect(got).toEqual({ kind: "cancelled" })
  })

  test("说选了却没说是哪个，就是服务端说错了", () => {
    const got = directoryPick({ picked: true, path: "  " }, en)
    expect(got.kind).toBe("failed")
  })

  test("路径两边的空格去掉", () => {
    const got = directoryPick({ picked: true, path: "  /tmp/picked \n" }, en)
    expect(got).toEqual({ kind: "picked", path: "/tmp/picked" })
  })
})
