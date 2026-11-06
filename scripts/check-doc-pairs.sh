#!/bin/bash
# The bilingual mirroring check for the document set.
#
# ★ Three checks per pair, and the identifier one is the strictest: the
#   backtick span has to match too, so "`POST /v2/run`" and "`POST`
#   `/v2/run`" are different sets.
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/docs" || exit 1

PAIRS=(
  "config-design.md config-design_zh.md"
  "mcp-design.md mcp-design_zh.md"
  "architecture.md architecture_zh.md"
  "user-manual.md user-manual_zh.md"
  "developer-manual.md developer-manual_zh.md"
)

fail=0
for p in "${PAIRS[@]}"; do
  set -- $p
  a=$1; b=$2
  id=$(diff <(grep -oP '`[a-zA-Z0-9_./:$-]+`' "$a" | sort -u) \
            <(grep -oP '`[a-zA-Z0-9_./:$-]+`' "$b" | sort -u))
  cb=$(diff <(awk '/^```/{f=!f;next}f' "$a") \
            <(awk '/^```/{f=!f;next}f' "$b"))
  sec=$(diff <(grep -oP '§[0-9]+(\.[0-9]+)?' "$a" | sort | uniq -c) \
             <(grep -oP '§[0-9]+(\.[0-9]+)?' "$b" | sort | uniq -c))

  printf '%-24s identifiers:%-4s codeblocks:%-4s sections:%-4s\n' "$a" \
    "$( [ -z "$id" ] && echo ok || echo DIFF )" \
    "$( [ -z "$cb" ] && echo ok || echo DIFF )" \
    "$( [ -z "$sec" ] && echo ok || echo DIFF )"
  [ -n "$id" ] && { fail=1; echo "$id" | head -20; }
  [ -n "$cb" ] && { fail=1; echo "$cb" | head -20; }
  [ -n "$sec" ] && { fail=1; echo "$sec" | head -20; }
done

echo
echo "── the line references ──"
refs=$(grep -ohP '`[a-zA-Z0-9_./-]+\.(go|ts|tsx|md):[0-9]+(-[0-9]+)?`' ./*.md | tr -d '`' | sort -u)
bad=0
while IFS= read -r ref; do
  [ -z "$ref" ] && continue
  path="${ref%%:*}"; start="${ref##*:}"; start="${start%%-*}"
  if [ -f "$ROOT/$path" ]; then
    total=$(wc -l < "$ROOT/$path")
    if [ "$start" -gt "$total" ]; then
      printf '  OUT OF RANGE  %s (the file has %s lines)\n' "$ref" "$total"; bad=1
    fi
    continue
  fi
  # ★ A short name: the document quotes the file by the name its own
  #   section uses. The reference holds if some file of that name has the
  #   line.
  base=$(basename "$path")
  found=0
  while IFS= read -r cand; do
    total=$(wc -l < "$cand")
    if [ "$start" -le "$total" ]; then found=1; break; fi
  done < <(find "$ROOT" -name "$base" -type f -not -path "*/node_modules/*" -not -path "*/.git/*")
  if [ "$found" -eq 0 ]; then
    printf '  BROKEN       %s\n' "$ref"; bad=1
  fi
done <<< "$refs"
[ "$bad" -eq 0 ] && echo "  every reference points at a line that exists"

exit $fail
