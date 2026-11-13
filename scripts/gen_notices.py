#!/usr/bin/env python3
"""Generate licenses/THIRD_PARTY_NOTICES.txt from the modules linked into a built binary.

usage: python3 scripts/gen_notices.py BINARY [BINARY...]   (or: make notices)
Pass the binaries of every platform you ship (linux, windows .exe, darwin): the
notices cover the union of their modules. Re-run whenever go.mod changes. The output is embedded in the app (About → Licenses).
"""
import os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
modcache = subprocess.check_output(["go", "env", "GOMODCACHE"], text=True).strip()
byname = {}
for binary in sys.argv[1:]:
    info = subprocess.check_output(["go", "version", "-m", binary], text=True)
    goversion = info.splitlines()[0].split(":", 1)[1].strip()
    cur = None
    for line in info.splitlines():
        f = line.strip().split("\t")
        if f[0] == "dep":
            cur = byname.setdefault(f[1], {"mod": f[1], "ver": f[2], "src": f[1], "srcver": f[2]})
        elif f[0] == "=>" and cur:
            cur["src"], cur["srcver"] = f[1], f[2]
deps = list(byname.values())

def esc(p):  # module cache escaping: upper case -> !lower
    return re.sub(r"[A-Z]", lambda m: "!" + m.group(0).lower(), p)

def moddir(path, ver):
    if path.startswith(("./", "../", "/")):  # local replacement (patched copy in third_party)
        d = os.path.normpath(os.path.join(ROOT, path))
        return (d, d) if os.path.isdir(d) else (None, None)
    parts = path.split("/")
    for i in range(len(parts), 0, -1):  # sub-module paths live inside the repo module
        d = os.path.join(modcache, esc("/".join(parts[:i])) + "@" + ver)
        if os.path.isdir(d):
            return os.path.join(d, *parts[i:]) if i < len(parts) else d, d
    return None, None

LIC = re.compile(r"^(LICEN[CS]E|COPYING|NOTICE|UNLICENSE)([.-].*)?$", re.I)

def kind(t):
    t = " ".join(t.split()).lower()
    if "academic free license" in t: return "AFL-2.0"
    if "gnu general public license" in t and "version 2, june 1991" in t: return "GPL-2.0"
    if "gnu general public license" in t and "version 3, 29 june 2007" in t: return "GPL-3.0"
    if "apache license" in t and "version 2.0" in t: return "Apache-2.0"
    if "mozilla public license" in t: return "MPL-2.0"
    if "gnu general public license" in t: return "GPL"
    if "sil open font license" in t: return "OFL-1.1"
    if "this is free and unencumbered software" in t: return "Unlicense"
    if "permission is hereby granted, free of charge" in t: return "MIT"
    if "permission to use, copy, modify, and/or distribute this software" in t: return "ISC"
    if "permission to use, copy, modify, and distribute this software" in t: return "ISC"
    if "neither the name" in t or "names of its contributors" in t: return "BSD-3-Clause"
    if "redistributions in binary form" in t: return "BSD-2-Clause"
    if "this software is provided 'as-is'" in t and "altered source versions" in t: return "zlib"
    return "see text"

def licfiles(d):
    out = []
    for root, dirs, files in os.walk(d):
        rel = os.path.relpath(root, d)
        depth = 0 if rel == "." else rel.count(os.sep) + 1
        dirs[:] = [x for x in dirs if depth < 4 and x not in ("testdata", "test", ".git")]
        for fn in sorted(files):
            if LIC.match(fn) and not fn.endswith((".go", ".c", ".h")):
                out.append(os.path.join(root, fn))
    return out

sections, summary = [], []

def add(name, url, lic, texts):
    summary.append((name, lic, url))
    body = "\n".join("--- %s ---\n%s" % (n, t.strip()) for n, t in texts)
    sections.append("=" * 78 + "\n%s\n%s\nLicense: %s\n\n%s\n" % (name, url, lic, body))

def read(p):
    with open(p, encoding="utf-8", errors="replace") as fh:
        return fh.read()

# vendored C tools
for name, url, d, lic in [
    ("hdl-dump", "https://github.com/ps2homebrew/hdl-dump", "third_party/hdl-dump", "GPL-2.0-or-later"),
    ("pfsshell (incl. parts of ps2sdk)", "https://github.com/ps2homebrew/pfsshell", "third_party/pfsshell",
     "GPL-2.0 (pfsshell) + AFL-2.0 (ps2sdk parts)"),
]:
    files = [os.path.join(ROOT, d, f) for f in sorted(os.listdir(os.path.join(ROOT, d))) if LIC.match(f)]
    texts = [(os.path.basename(p), read(p)) for p in files]
    add(name, url, lic, texts)

# Go modules
for dep in sorted(deps, key=lambda x: x["mod"].lower()):
    d, top = moddir(dep["src"], dep["srcver"])
    if not d:
        print("missing module dir:", dep, file=sys.stderr); sys.exit(1)
    files = licfiles(top)
    if not files:
        print("no license file:", dep["mod"], file=sys.stderr); sys.exit(1)
    texts = [(os.path.relpath(p, top), read(p)) for p in files]
    kinds = sorted({kind(t) for _, t in texts} - {"see text"}) or ["see text"]
    local = dep["src"].startswith(("./", "../", "/"))
    url = "https://" + (dep["mod"] if local else dep["src"]).split("/v3.3")[0]
    label = "%s %s" % (dep["mod"], dep["ver"]) + (" (patched copy in %s)" % dep["src"].lstrip("./") if local else "")
    add(label, url, " + ".join(kinds), texts)

# Go standard library / runtime
goroot = subprocess.check_output(["go", "env", "GOROOT"], text=True).strip()
add("Go standard library and runtime (%s)" % goversion, "https://go.dev", "BSD-3-Clause",
    [("LICENSE", read(os.path.join(goroot, "LICENSE")))])

head = ["THIRD-PARTY SOFTWARE NOTICES", "",
        "PS2 HDL Manager includes the following third-party software. Each component",
        "is distributed under its own license, reproduced in full below.", "",
        "Algorithms of the CHD reader/writer follow MAME (BSD-3-Clause, Copyright Aaron Giles",
        "and the MAME team); the CD ECC tables are generated from MAME's cdrom.cpp.", "",
        "SUMMARY", ""]
w = max(len(n) for n, _, _ in summary)
for n, l, u in summary:
    head.append("  %-*s  %s" % (w, n, l))
os.makedirs(os.path.join(ROOT, "licenses"), exist_ok=True)
with open(os.path.join(ROOT, "licenses", "THIRD_PARTY_NOTICES.txt"), "w") as fh:
    fh.write("\n".join(head) + "\n\n" + "\n".join(sections))
print("%d components" % len(summary))
