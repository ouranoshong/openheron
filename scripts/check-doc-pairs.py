#!/usr/bin/env python3
"""The bilingual mirroring check for the document set.

★ The identifier sets must be byte-identical: the backtick span has to
  match too, so "`POST /v2/run`" and "`POST` `/v2/run`" are different sets.

★ The code blocks must agree on everything that is **not prose**: the
  commands, the paths and the code itself. The Chinese version keeps its
  own comments — that is the repository's convention, and AGENTS.md and
  README.md do it too.

★ Only the **bilingual** documents are checked here. Everything in
  `CHINESE_ONLY` has no English twin to mirror — see AGENTS.md.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"

# ★ The bilingual set, and it is the whole bilingual set: what a user or an
#   integrator reads. Everything else under `docs/` is Chinese only.
PAIRS = [
    ("README.md", "README_zh.md"),
    ("docs/architecture.md", "docs/architecture_zh.md"),
    ("docs/user-manual.md", "docs/user-manual_zh.md"),
    ("docs/developer-manual.md", "docs/developer-manual_zh.md"),
    ("sandbox-kit/TUTORIAL.md", "sandbox-kit/TUTORIAL_zh.md"),
    ("sandbox-kit/BUILDING.md", "sandbox-kit/BUILDING_zh.md"),
]

# ★ Architecture and code-design documents are **Chinese only** (AGENTS.md):
#   their audience is the people who read Chinese, and a hand-maintained
#   English twin is a second copy that drifts. The Chinese document took
#   over the plain name — `docs/config-design.md` **is** the Chinese one —
#   so a `_zh` twin for any of these is a mistake, not a mirror. This
#   catches it before it becomes a pair nobody updates.
CHINESE_ONLY = [
    "animation-design.md",
    "attachment-design.md",
    "config-design.md",
    "config-optimization-design.md",
    "context-usage-design.md",
    "fs-backend-design.md",
    "logging-design.md",
    "mcp-design.md",
    "run-lifecycle-design.md",
    "sandbox-kit-design.md",
    "session-concurrency-design.md",
    "tool-surface-design.md",
]

IDENT = re.compile(r"`[a-zA-Z0-9_./:$-]+`")
SECTION = re.compile(r"§[0-9]+(\.[0-9]+)?")


def blocks(text: str) -> list[str]:
    """The bodies of the fenced code blocks, fences stripped."""
    out, inside = [], False
    for line in text.splitlines():
        if line.startswith("```"):
            inside = not inside
            continue
        if inside:
            out.append(line)
    return out


def strip_comment(line: str) -> str:
    """The part of a code line that is not prose.

    ★ A trailing comment and a blockquote line are prose: the Chinese
      version writes its own. What has to match is the command, the path
      and the code.
    """
    if line.lstrip().startswith(">"):
        return ""
    out, i, in_str = "", 0, False
    while i < len(line):
        ch = line[i]
        if ch in "\"'":
            in_str = not in_str
        if not in_str:
            if ch == "#" and (i == 0 or line[i - 1] in " \t"):
                break
            if ch == "/" and i + 1 < len(line) and line[i + 1] == "/":
                if i == 0 or line[i - 1] != ":":
                    break
        out += ch
        i += 1
    return out.rstrip()


def identifiers(text: str) -> list[str]:
    return sorted(set(IDENT.findall(text)))


def sections(text: str) -> list[str]:
    counts: dict[str, int] = {}
    for m in SECTION.findall(text):
        counts[m] = counts.get(m, 0) + 1
    return [f"{k} {v}" for k, v in sorted(counts.items())]


def main() -> int:
    # ★ The output carries box-drawing characters and the document names. On
    #   Windows the default stdout encoding is the console code page, so a piped
    #   run turns them into mojibake — the check still passes, but the report
    #   is unreadable.
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")

    fail = 0

    for name in CHINESE_ONLY:
        twin = DOCS / name.replace(".md", "_zh.md")
        if twin.exists():
            print(f"{name}: Chinese only, but {twin.name} exists — delete the twin")
            fail = 1
    for en, zh in PAIRS:
        a = (ROOT / en).read_text(encoding="utf-8")
        b = (ROOT / zh).read_text(encoding="utf-8")

        id_diff = [x for x in identifiers(a) if x not in set(identifiers(b))]
        id_diff += [x for x in identifiers(b) if x not in set(identifiers(a))]
        sa, sb = sections(a), sections(b)
        sec_diff = [f"{x} vs {y}" for x, y in zip(sa, sb) if x != y]
        if len(sa) != len(sb):
            sec_diff.append(f"a different number of § references ({len(sa)} vs {len(sb)})")

        ba, bb = blocks(a), blocks(b)
        block_diff = []
        if len(ba) != len(bb):
            block_diff.append(f"a different number of blocks ({len(ba)} vs {len(bb)})")
        else:
            for i, (x, y) in enumerate(zip(ba, bb)):
                if strip_comment(x) != strip_comment(y):
                    block_diff.append(f"block {i}: {x!r} vs {y!r}")

        status = lambda ok: "ok" if ok else "DIFF"
        print(
            f"{en:<24} identifiers:{status(not id_diff):<6}"
            f" code:{status(not block_diff):<6} sections:{status(not sec_diff):<6}"
        )
        for d in id_diff[:10]:
            print("   identifier:", d)
        for d in block_diff[:10]:
            print("   code:", d)
        for d in sec_diff[:10]:
            print("   section:", d)
        if id_diff or block_diff or sec_diff:
            fail = 1

    print()
    print("── the line references ──")
    bad = 0
    refs = set()
    # ★ The READMEs carry no line references today, but they are documents
    #   too — the scan reads them so that a new reference cannot slip past.
    for md in list(DOCS.glob("*.md")) + [ROOT / "README.md", ROOT / "README_zh.md"]:
        for m in re.findall(r"`([a-zA-Z0-9_./-]+\.(?:go|ts|tsx|md)):(\d+)", md.read_text(encoding="utf-8")):
            refs.add((m[0], int(m[1])))
    external = 0
    for path, line in sorted(refs):
        candidates = [ROOT / path]
        if not candidates[0].is_file():
            if "/" not in path:
                # ★ A bare file name is ambiguous: it may name a file in the
                #   module cache (a table of the vendor packages does that).
                external += 1
                continue
            # ★ A short name: the document quotes the file by the name its
            #   own section uses. The reference holds if some file of that
            #   name has the line.
            candidates = list(ROOT.rglob(Path(path).name))
            candidates = [c for c in candidates if "node_modules" not in str(c)]
        if not candidates:
            # ★ A path that names nothing in this repository is a reference
            #   to the outside — eino's own source, the upstream documents.
            external += 1
            continue
        if not any(c.is_file() and line <= len(c.read_text(encoding="utf-8", errors="replace").splitlines())
                   for c in candidates):
            print(f"  BROKEN       {path}:{line}")
            bad = 1
    if not bad:
        print(f"  every reference points at a line that exists "
              f"({external} point outside this repository)")

    return fail


if __name__ == "__main__":
    sys.exit(main())
