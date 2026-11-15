import { describe, expect, test } from "bun:test"

import {
  NO_PROJECT_GROUP,
  PINNED_GROUP,
  defaultProject,
  directoryPick,
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

describe("groupSessions —— 多项目模式", () => {
  test("置顶组在最前，项目按 order，未选项目在最后", () => {
    const groups = groupSessions(
      [session("a", "beta"), session("b", "alpha"), session("c"), session("d", "beta", true)],
      [project("beta", 2), project("alpha", 1)],
      null,
    )
    expect(groups.map((g) => g.key)).toEqual([PINNED_GROUP, "alpha", "beta", NO_PROJECT_GROUP])
    expect(groups[0]!.sessions.map((s) => s.meta.threadId)).toEqual(["d"])
    expect(groups[1]!.sessions.map((s) => s.meta.threadId)).toEqual(["b"])
    expect(groups[2]!.sessions.map((s) => s.meta.threadId)).toEqual(["a"])
    expect(groups[3]!.sessions.map((s) => s.meta.threadId)).toEqual(["c"])
  })

  test("组标题是项目的名字", () => {
    const groups = groupSessions([session("a", "alpha")], [project("alpha", 0, "CodeLab")], null)
    expect(groups[0]!.title).toBe("CodeLab")
  })

  test("空组不出现", () => {
    const groups = groupSessions([session("a", "alpha")], [project("alpha", 0), project("empty", 1)], null)
    expect(groups.map((g) => g.key)).toEqual(["alpha"])
  })

  test("一个会话只在一个组里", () => {
    const groups = groupSessions([session("a", "alpha", true)], [project("alpha", 0)], null)
    expect(groups.map((g) => g.sessions.length)).toEqual([1])
    expect(groups[0]!.key).toBe(PINNED_GROUP)
  })

  test("表里没有的项目 id 落到未选项目组", () => {
    const groups = groupSessions([session("a", "gone")], [project("alpha", 0)], null)
    expect(groups.map((g) => g.key)).toEqual([NO_PROJECT_GROUP])
  })

  test("没有项目也没有会话时没有组", () => {
    expect(groupSessions([], [], null)).toEqual([])
  })
})

describe("groupSessions —— 单项目模式", () => {
  test("只有一组：作用域项目 + 未选项目的会话", () => {
    const groups = groupSessions(
      [session("a", "alpha"), session("b"), session("c", "other")],
      [project("alpha", 0), project("other", 1)],
      scope("alpha"),
    )
    expect(groups).toHaveLength(1)
    expect(groups[0]!.key).toBe("alpha")
    expect(groups[0]!.title).toBe("alpha")
    expect(groups[0]!.sessions.map((s) => s.meta.threadId)).toEqual(["a", "b"])
  })

  test("别的项目的会话不进来", () => {
    const groups = groupSessions([session("c", "other")], [project("other", 0)], scope("alpha"))
    expect(groups).toEqual([])
  })

  test("只有未选项目的会话时也在那一组里", () => {
    const groups = groupSessions([session("b")], [], scope("alpha"))
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

describe("defaultProject", () => {
  test("多项目模式取第一个", () => {
    expect(defaultProject([project("b", 1), project("a", 0)], null)?.id).toBe("a")
  })

  test("单项目模式取作用域", () => {
    expect(defaultProject([project("b", 1)], scope("b"))?.id).toBe("b")
  })

  test("表里还没有这个项目时也答得出作用域", () => {
    expect(defaultProject([], scope("b"))?.id).toBe("b")
  })
})

describe("resolveActiveProject", () => {
  test("单项目模式由作用域说了算", () => {
    expect(resolveActiveProject("alpha", [project("alpha", 0), project("beta", 1)], scope("beta"))).toBe("beta")
  })

  test("多项目模式保留用户选的项目", () => {
    expect(resolveActiveProject("beta", [project("alpha", 0), project("beta", 1)], null)).toBe("beta")
  })

  test("选的项目没了就退回第一个", () => {
    expect(resolveActiveProject("gone", [project("alpha", 0)], null)).toBe("alpha")
  })

  test("一个项目都没有时没有答案", () => {
    expect(resolveActiveProject(null, [], null)).toBeNull()
  })

  test("「没有项目」是一个选择 —— 刷新不能把它改掉", () => {
    // ★ The sidebar offers "No project", and the server accepts a
    //   conversation without one — it works in the default project root.
    //   ★ A refresh must not turn that choice back into the first project.
    expect(resolveActiveProject("", [project("alpha", 0), project("beta", 1)], null)).toBe("")
  })

  test("还没选过（null）才退回第一个项目", () => {
    expect(resolveActiveProject(null, [project("alpha", 0), project("beta", 1)], null)).toBe("alpha")
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
    expect(projectNameError("")).toBeUndefined()
    expect(projectNameError("   ")).toBeUndefined()
  })

  test("名字只能是一行", () => {
    // ★ The sidebar draws the name as a heading, and a name with a newline
    //   in it breaks that heading into two.
    expect(projectNameError("api\ngateway")).toBeDefined()
    expect(projectNameError("api\r\ngateway")).toBeDefined()
  })

  test("普通名字没有理由", () => {
    expect(projectNameError("CodeLab")).toBeUndefined()
  })
})

describe("projectRenameError", () => {
  test("重命名时名字是必填的 —— 项目本来就有名字", () => {
    // ★ Renaming is the one place where the name is required: the server
    //   refuses an empty one ("the project name must not be empty").
    expect(projectRenameError("")).toBeDefined()
    expect(projectRenameError("  ")).toBeDefined()
  })

  test("重命名也是一行", () => {
    expect(projectRenameError("api\ngateway")).toBeDefined()
  })

  test("普通名字没有理由", () => {
    expect(projectRenameError("CodeLab")).toBeUndefined()
  })
})

describe("projectRootError", () => {
  test("目录是唯一必填项，空的不行", () => {
    expect(projectRootError("")).toBeDefined()
    expect(projectRootError("  ")).toBeDefined()
  })

  test("目录不检查存不存在 —— 那是服务端的事", () => {
    // ★ The client must not keep a second copy of the rules: the server
    //   resolves the path, and it is the one that says no.
    expect(projectRootError("/tmp/does-not-exist")).toBeUndefined()
  })
})

describe("directoryPick", () => {
  test("选中了就是那条路径", () => {
    const got = directoryPick({ picked: true, path: "/tmp/picked" })
    expect(got).toEqual({ kind: "picked", path: "/tmp/picked" })
  })

  test("关掉选择器不是失败", () => {
    // ★ "Not now" is how a person says it, and the client must not report
    //   it as an error.
    const got = directoryPick({ picked: false, path: "" })
    expect(got).toEqual({ kind: "cancelled" })
  })

  test("说选了却没说是哪个，就是服务端说错了", () => {
    const got = directoryPick({ picked: true, path: "  " })
    expect(got.kind).toBe("failed")
  })

  test("路径两边的空格去掉", () => {
    const got = directoryPick({ picked: true, path: "  /tmp/picked \n" })
    expect(got).toEqual({ kind: "picked", path: "/tmp/picked" })
  })
})
