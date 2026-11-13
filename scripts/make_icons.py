#!/usr/bin/env python3
"""Render the app icon (assets/icon/icon.svg) for every platform.

Outputs, all committed so normal builds need none of these tools:
  assets/icon/icon-<N>.png   PNGs 16…1024 (window icon, Linux, README)
  assets/icon/icon.ico       Windows (16–256, PNG entries)
  assets/icon/icon.icns      macOS app bundle
Needs rsvg-convert (librsvg). Run: make icons
"""
import os, struct, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIR = os.path.join(ROOT, "assets", "icon")
SVG = os.path.join(DIR, "icon.svg")
SIZES = [16, 24, 32, 48, 64, 128, 256, 512, 1024]


def render(n):
    out = os.path.join(DIR, f"icon-{n}.png")
    subprocess.run(["rsvg-convert", "-w", str(n), "-h", str(n), SVG, "-o", out], check=True)
    with open(out, "rb") as f:
        return f.read()


def ico(pngs):
    sizes = [16, 24, 32, 48, 64, 128, 256]
    head = struct.pack("<HHH", 0, 1, len(sizes))
    off = 6 + 16 * len(sizes)
    entries, data = b"", b""
    for n in sizes:
        b = pngs[n]
        dim = 0 if n == 256 else n  # 0 means 256
        entries += struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(b), off + len(data))
        data += b
    return head + entries + data


def icns(pngs):
    # PNG-based entries understood by macOS 10.7+
    types = [(b"icp4", 16), (b"icp5", 32), (b"icp6", 64), (b"ic07", 128), (b"ic08", 256),
             (b"ic09", 512), (b"ic10", 1024), (b"ic11", 32), (b"ic12", 64), (b"ic13", 256), (b"ic14", 512)]
    body = b"".join(t + struct.pack(">I", 8 + len(pngs[n])) + pngs[n] for t, n in types)
    return b"icns" + struct.pack(">I", 8 + len(body)) + body


def main():
    pngs = {n: render(n) for n in SIZES}
    with open(os.path.join(DIR, "icon.ico"), "wb") as f:
        f.write(ico(pngs))
    with open(os.path.join(DIR, "icon.icns"), "wb") as f:
        f.write(icns(pngs))
    print("icons written to", os.path.relpath(DIR, ROOT))


if __name__ == "__main__":
    sys.exit(main())
