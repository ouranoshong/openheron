import { describe, expect, test } from "bun:test"

import { BOTTOM_TOLERANCE, followAfterScroll } from "../src/lib/scroll-follow"

const viewport = 600

function at(top: number, height: number) {
  return { top, height }
}

describe("followAfterScroll", () => {
  test("停在底部时永远重新挂上跟随，哪怕用户刚往上滚过", () => {
    const out = followAfterScroll({
      sample: at(900, 1500),
      previous: at(100, 900),
      clientHeight: viewport,
      pinned: false,
      userMovedUp: true,
    })
    expect(out.pinned).toBe(true)
  })

  test("用户往上翻就断开跟随", () => {
    const out = followAfterScroll({
      sample: at(200, 1000),
      previous: at(400, 1000),
      clientHeight: viewport,
      pinned: true,
      userMovedUp: false,
    })
    expect(out.pinned).toBe(false)
  })

  test("★ 内容变高时浏览器自己挪的滚动位置不算用户离开", () => {
    // 900 -> 1500：一块高内容落地，浏览器顺手挪了滚动位置，离底部 200px。
    const out = followAfterScroll({
      sample: at(300, 1500),
      previous: at(400, 900),
      clientHeight: viewport,
      pinned: true,
      userMovedUp: false,
    })
    expect(out.pinned).toBe(true)
  })

  test("★ 内容变高的同一批里用户往上翻，仍然以用户为准", () => {
    const out = followAfterScroll({
      sample: at(300, 1500),
      previous: at(400, 900),
      clientHeight: viewport,
      pinned: true,
      userMovedUp: true,
    })
    expect(out.pinned).toBe(false)
  })

  test("已经断开时不因为内容变高而被拽回来", () => {
    const out = followAfterScroll({
      sample: at(0, 1500),
      previous: at(0, 900),
      clientHeight: viewport,
      pinned: false,
      userMovedUp: false,
    })
    expect(out.pinned).toBe(false)
  })

  test("容差内仍算在底部", () => {
    const out = followAfterScroll({
      sample: at(400 - (BOTTOM_TOLERANCE - 1), 1000),
      previous: at(400, 1000),
      clientHeight: viewport,
      pinned: false,
      userMovedUp: true,
    })
    expect(out.pinned).toBe(true)
  })

  test("容差外就是离开了", () => {
    const out = followAfterScroll({
      sample: at(400 - (BOTTOM_TOLERANCE + 1), 1000),
      previous: at(400, 1000),
      clientHeight: viewport,
      pinned: true,
      userMovedUp: false,
    })
    expect(out.pinned).toBe(false)
  })
})
