#!/usr/bin/env python3
"""从 shadcn registry 拉取组件并写入项目。

shadcn CLI 走 ui.shadcn.com/init，openheron 网络到该host 要 12s+、超过 CLI 内置
的 10s 超时而失败；assistant-ui 的 registry 又依赖 shadcn CLI 解析依赖链。
本脚本替代它：直接按 registry-item schema 递归解析 registryDependencies，
写入 files[].path，并在最后汇总 npm 依赖。

用法：
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

# components.json 的 aliases → 实际目录。registry item 给的 path 是「alias 根」
# 形式（如 components/assistant-ui/… 、hooks/…），shadcn 的 ui 基础件则给
# 仓库源路径（registry/<style>/ui/…），两者的落地位置都由这张表决定。
ALIASES = {
    "components": "src/components",
    "ui": "src/components/ui",
    "utils": "src/lib",
    "lib": "src/lib",
    "hooks": "src/hooks",
}

# registry 的 dependencies 里会混入 shadcn 的 utils 别名，那不是 npm 包
NOT_NPM = {"cn"}


def rewrite_imports(content: str) -> str:
    """registry 给的源码用的是 shadcn CLI 的**未解析**导入写法，CLI 落地时才会改写。
    这里手工做同样的改写，否则 TypeScript 解析不到。"""
    # from "cn" → shadcn 的 utils 别名
    content = content.replace('from "cn"', 'from "@/lib/utils"')
    content = content.replace("from 'cn'", "from '@/lib/utils'")
    # from "@/registry/<style>/ui/x" → 别名根下的 ui
    content = re.sub(r'from "@/registry/[^"]+/ui/([\w.-]+)"', r'from "@/components/ui/\1"', content)
    # shadcn 内部开发组件，落在 components/ui 下（见 icon-placeholder.tsx）
    content = content.replace(
        'from "@/app/(create)/components/icon-placeholder"',
        'from "@/components/ui/icon-placeholder"',
    )
    return content


def apply_aliases(rel: str) -> Path:
    """把 alias 根路径映射到真实目录。"""
    head, _, tail = rel.partition("/")
    if head in ALIASES:
        rel = f"{ALIASES[head]}/{tail}" if tail else ALIASES[head]
    return PROJECT / rel


def target_path(f: dict) -> Path:
    """决定一个 registry file 的落地路径。"""
    target = f.get("target")
    if target:
        if target.startswith("@/"):
            return apply_aliases(target[2:])
        return PROJECT / target

    path = f["path"]
    # shadcn 基础件给的是仓库源路径，按 ui 别名落
    if path.startswith("registry/"):
        return apply_aliases(f"ui/{Path(path).name}")
    return apply_aliases(path)


def fetch(url: str) -> dict:
    req = urllib.request.Request(url, headers={"User-Agent": "openheron-fetch-registry"})
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        return json.loads(r.read().decode())


def resolve(name: str) -> str | None:
    """裸名 → registry URL。先试 shadcn 基础件，再试 assistant-ui。"""
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
                print(f"  跳过（已存在） {rel}")
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(rewrite_imports(f.get("content", "")), encoding="utf-8")
                files_written.append(str(rel))
                print(f"  写入 {rel}")

        # registry-item 可以声明 css / cssVars（shadcn 的主题注入）
        css = item.get("css")
        if isinstance(css, dict) and css.get("path"):
            path = apply_aliases(css["path"])
            if force or not path.exists():
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(css.get("content", ""), encoding="utf-8")
                files_written.append(str(path.relative_to(PROJECT)))
                print(f"  写入 {path.relative_to(PROJECT)}  (css)")
        elif isinstance(css, str) and css.strip():
            print(f"  ! {item_name} 声明了内联 css，需手工合入 src/index.css")

        for dep in item.get("registryDependencies") or []:
            target = resolve(dep)
            if target is None:
                missing.append(f"{dep}  (依赖解析失败)")
            else:
                visit(target, depth + 1)

    for r in roots:
        url = resolve(r)
        if url is None:
            missing.append(f"{r}  (根项解析失败)")
            continue
        print(f"→ {r}")
        visit(url)

    print(f"\n文件 {len(files_written)} 个 · registry 项 {len(seen)} 个")
    if missing:
        print("未能解析：")
        for m in missing:
            print(f"  ✗ {m}")
    if deps:
        print("\nnpm 依赖：")
        for d in sorted(deps):
            print(f"  {d}")
    return 1 if missing else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))