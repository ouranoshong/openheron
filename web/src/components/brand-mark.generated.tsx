// ★ GENERATED FILE — do not edit it by hand.
//
//   Source:      public/favicon.svg
//   Regenerate:  bun run gen:brand
//   Verify:      bun run gen:brand:check
//
// ★ The favicon and the interface's mark are one drawing. Two hand-kept copies
//   drift, so the SVG is the source and this file is written from it.
import { useId } from "react"

export function BrandMarkGlyph({
  className,
  decorative = false,
  testId,
}: {
  className?: string
  decorative?: boolean
  testId?: string
}) {
  // ★ The gradient and the two masks are referenced by id, and the page can
  //   hold more than one mark (the header and the welcome screen). React's
  //   useId gives every instance its own prefix, so the second mark does not
  //   resolve its mask to the first mark's.
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, "")
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 48 48"
      role={decorative ? undefined : "img"}
      aria-hidden={decorative ? true : undefined}
      aria-label={decorative ? undefined : "openheron"}
      data-testid={testId}
      className={className}
    >
      <title>openheron</title>
      <defs>
        <linearGradient
          id={uid + "-brand"}
          x1="3"
          y1="3"
          x2="45"
          y2="45"
          gradientUnits="userSpaceOnUse"
        >
          <stop stopColor="#e6ff5c" />
          <stop offset=".55" stopColor="#dcfd35" />
          <stop offset="1" stopColor="#8fe3ff" />
        </linearGradient>
        <mask
          id={uid + "-body"}
          maskUnits="userSpaceOnUse"
          x="0"
          y="0"
          width="48"
          height="48"
        >
          <g transform="translate(24 24) scale(1.3) translate(-28.2 -24.5)">
            <g
              className="mark-parts"
              fill="#fff"
            >
              <path className="mark-beak" d="M12 11.5 L43 8 L12 17 Z" />
              <path className="mark-neck" d="M12.5 16 L19 16.4 C19.4 24 19.8 32 20.6 41 L13.6 41 C13.8 32 13 24 12.5 16 Z" />
              <rect className="mark-stem" x="28" y="13" width="6.6" height="28" rx="1.4" />
              <rect className="mark-bar" x="12.5" y="24" width="15.5" height="6.6" rx="1.4" />
              <circle className="mark-eye" cx="17.6" cy="13.4" r="1.55" fill="#000" />
            </g>
          </g>
        </mask>
        <mask
          id={uid + "-halo"}
          maskUnits="userSpaceOnUse"
          x="0"
          y="0"
          width="48"
          height="48"
        >
          <g transform="translate(24 24) scale(1.365) translate(-28.2 -24.5)">
            <g
              className="mark-parts"
              fill="#fff"
            >
              <path className="mark-beak" d="M12 11.5 L43 8 L12 17 Z" />
              <path className="mark-neck" d="M12.5 16 L19 16.4 C19.4 24 19.8 32 20.6 41 L13.6 41 C13.8 32 13 24 12.5 16 Z" />
              <rect className="mark-stem" x="28" y="13" width="6.6" height="28" rx="1.4" />
              <rect className="mark-bar" x="12.5" y="24" width="15.5" height="6.6" rx="1.4" />
            </g>
          </g>
        </mask>
      </defs>
      <rect width="48" height="48" fill="#040319" mask={`url(#${uid}-halo)`} />
      <rect width="48" height="48" fill={`url(#${uid}-brand)`} mask={`url(#${uid}-body)`} />
      <g
        stroke={`url(#${uid}-brand)`}
        strokeWidth="1.9"
        fill="none"
        strokeLinejoin="miter"
        transform="translate(24 24) scale(1.3) translate(-28.2 -24.5)"
      >
        <path className="mark-trace" pathLength="1" d="M34.6 27.3 L40 27.3 L40 17.5" />
      </g>
      <circle className="mark-dot" cx="42.6" cy="17.5" r="2.2" fill="#040319" transform="translate(24 24) scale(1.3) translate(-28.2 -24.5)" />
      <circle className="mark-dot" cx="42.6" cy="17.5" r="1.7" fill="#8fe3ff" transform="translate(24 24) scale(1.3) translate(-28.2 -24.5)" />
    </svg>
  )
}
