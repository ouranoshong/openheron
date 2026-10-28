#!/usr/bin/env python3
"""Fetch components from the shadcn registry and write them into the project.

The shadcn CLI goes through ui.shadcn.com/init, which takes openheron's network
12s+ — over the CLI's built-in 10s timeout, so it fails; assistant-ui's registry
also depends on the shadcn CLI to resolve the dependency chain.
This script replaces it: it resolves registryDependencies recursively straight
from the registry-item schema, writes files[].path, and finally summarizes the
npm dependencies.

Usage:
    python3 scripts/fetch-registry.py thread thread-list ...
"""
import json
import re
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

PROJECT = Path(__file__).resolve().parent.parent

SHADCN = "https://ui.shadcn.com/r/styles/radix-nova/{name}.json"
ASSISTANT_UI = "https://r.assistant-ui.com/styles/radix/{name}.json"

TIMEOUT = 30

# components.json aliases → real directories. A registry item's path is given in
# "alias root" form (e.g. components/assistant-ui/… , hooks/…), while shadcn's ui
# primitives come with a repo source path (registry/<style>/ui/…); where each of
# them lands is decided by this table.
ALIASES = {
    "components": "src/components",
    "ui": "src/components/ui",
    "utils": "src/lib",
    "lib": "src/lib",
    "hooks": "src/hooks",
}

# shadcn's utils alias sneaks into a registry item's dependencies; it is not an npm package
NOT_NPM = {"cn"}


def rewrite_imports(content: str) -> str:
    """The source a registry hands over uses shadcn CLI's **unresolved** import
    form; the CLI rewrites it when it writes the files. We do the same rewrite by
    hand here, otherwise TypeScript cannot resolve them."""
    # from "cn" → shadcn's utils alias
    content = content.replace('from "cn"', 'from "@/lib/utils"')
    content = content.replace("from 'cn'", "from '@/lib/utils'")
    # from "@/registry/<style>/ui/x" → the ui directory under the alias root
    content = re.sub(r'from "@/registry/[^"]+/ui/([\w.-]+)"', r'from "@/components/ui/\1"', content)
    # shadcn's internal dev component, which lands under components/ui (see icon-placeholder.tsx)
    content = content.replace(
        'from "@/app/(create)/components/icon-placeholder"',
        'from "@/components/ui/icon-placeholder"',
    )
    return content


def apply_aliases(rel: str) -> Path:
    """Map an alias root path onto its real directory."""
    head, _, tail = rel.partition("/")
    if head in ALIASES:
        rel = f"{ALIASES[head]}/{tail}" if tail else ALIASES[head]
    return PROJECT / rel


def target_path(f: dict) -> Path:
    """Decide where a registry file lands."""
    target = f.get("target")
    if target:
        if target.startswith("@/"):
            return apply_aliases(target[2:])
        return PROJECT / target

    path = f["path"]
    # shadcn's primitives come with a repo source path, so they land under the ui alias
    if path.startswith("registry/"):
        return apply_aliases(f"ui/{Path(path).name}")
    return apply_aliases(path)


def fetch(url: str) -> dict:
    req = urllib.request.Request(url, headers={"User-Agent": "openheron-fetch-registry"})
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        return json.loads(r.read().decode())


def resolve(name: str) -> str | None:
    """Bare name → registry URL. Try shadcn's primitives first, then assistant-ui."""
    if name.startswith("http"):
        return name
    for tpl in (SHADCN, ASSISTANT_UI):
        url = tpl.format(name=name)
        try:
            fetch(url)
            return url
        except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError):
            continue
    return None


def main(argv: list[str]) -> int:
    roots = [a for a in argv if not a.startswith("-")]
    force = "-f" in argv
    if not roots:
        print(__doc__)
        return 2

    files_written: list[str] = []
    deps: set[str] = set()
    seen: set[str] = set()
    missing: list[str] = []

    def visit(url: str, depth: int = 0) -> None:
        if url in seen:
            return
        seen.add(url)
        try:
            item = fetch(url)
        except Exception as e:  # noqa: BLE001
            missing.append(f"{url}  ({e})")
            return

        item_name = item.get("name", url)
        deps.update(d for d in (item.get("dependencies") or []) if d not in NOT_NPM)
        deps.update(d for d in (item.get("devDependencies") or []) if d not in NOT_NPM)

        for f in item.get("files") or []:
            path = target_path(f)
            rel = path.relative_to(PROJECT)
            if path.exists() and not force:
                print(f"  skipped (already exists) {rel}")
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(rewrite_imports(f.get("content", "")), encoding="utf-8")
                files_written.append(str(rel))
                print(f"  wrote {rel}")

        # A registry item may declare css / cssVars (shadcn's theme injection)
        css = item.get("css")
        if isinstance(css, dict) and css.get("path"):
            path = apply_aliases(css["path"])
            if force or not path.exists():
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(css.get("content", ""), encoding="utf-8")
                files_written.append(str(path.relative_to(PROJECT)))
                print(f"  wrote {path.relative_to(PROJECT)}  (css)")
        elif isinstance(css, str) and css.strip():
            print(f"  ! {item_name} declares inline css, it must be merged into src/index.css by hand")

        for dep in item.get("registryDependencies") or []:
            target = resolve(dep)
            if target is None:
                missing.append(f"{dep}  (dependency resolution failed)")
            else:
                visit(target, depth + 1)

    for r in roots:
        url = resolve(r)
        if url is None:
            missing.append(f"{r}  (root item resolution failed)")
            continue
        print(f"→ {r}")
        visit(url)

    print(f"\n{len(files_written)} files · {len(seen)} registry items")
    if missing:
        print("could not resolve:")
        for m in missing:
            print(f"  ✗ {m}")
    if deps:
        print("\nnpm dependencies:")
        for d in sorted(deps):
            print(f"  {d}")
    return 1 if missing else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
