// formatSize is the size a human reads.
//
// ★ The units are binary, because that is what the sizes on disk are: a
//   file the server calls "too big" must not read as "just under" here.
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return ""
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
