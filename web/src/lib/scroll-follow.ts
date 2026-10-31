// ★ Whether the view follows new content is decided here, as a pure function,
//   so the rule can be pinned by `bun test` instead of by clicking around.
//
//   The rule used to be "follow while the last scroll event was at the
//   bottom". Any scroll event that was not the user then cleared the pin, and
//   once cleared only scrolling back to the bottom re-armed it. But when a
//   tall block lands, the browser moves the scroll position itself — a clamp,
//   a scroll anchoring adjustment, a smooth scroll still in flight — so the
//   view could stop following exactly when the content started growing the
//   most, and the user had no reason to scroll, which is why it never
//   re-armed.

export const BOTTOM_TOLERANCE = 32

export interface ScrollSample {
  top: number
  height: number
}

export interface FollowInput {
  /** Where the scroller is now — the scroll event being handled. */
  sample: ScrollSample
  /** Where it was at the previous scroll event. */
  previous: ScrollSample
  clientHeight: number
  /** Was the view pinned to the bottom before this event? */
  pinned: boolean
  /** Did the user's own input (wheel / touch) point up before this event? */
  userMovedUp: boolean
}

export interface FollowDecision {
  pinned: boolean
  /** The sample to remember as `previous` for the next event. */
  sample: ScrollSample
}

export function atBottom(
  sample: ScrollSample,
  clientHeight: number,
  tolerance: number = BOTTOM_TOLERANCE,
): boolean {
  return sample.height - sample.top - clientHeight < tolerance
}

export function followAfterScroll(i: FollowInput): FollowDecision {
  const { sample, previous, clientHeight, pinned, userMovedUp } = i

  // Reaching the bottom is the one event that always re-arms the follow, so
  // the view heals itself without the user having to find a button.
  if (atBottom(sample, clientHeight)) return { pinned: true, sample }

  // A content change and a scroll change in the same batch mean the browser
  // moved the view, not the user: keep the pin whatever it was. The user's own
  // upward input still wins, or a streaming reply would be impossible to read
  // from the top.
  const contentChanged = sample.height !== previous.height
  if (contentChanged && !userMovedUp) return { pinned, sample }

  return { pinned: false, sample }
}
