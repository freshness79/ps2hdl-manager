#!/usr/bin/env python3
"""Reference compressed images for the format tests, written independently of the
Go code under test: CSO v1 (raw deflate), CSO v2 (deflate + LZ4 blocks) and ZSO
(LZ4 blocks, encoded with the python-lz4 library).

usage: make_test_images.py SOURCE.iso OUTDIR   (needs: pip install lz4)
"""
import os, struct, sys, zlib
import lz4.block

def write(path, magic, ver, src, block, compress, align=0):
    n = (len(src) + block - 1) // block
    index, chunks = [], []
    pos = 24 + (n + 1) * 4
    pos = (pos + (1 << align) - 1) >> align << align
    head_len = pos
    for i in range(n):
        raw = src[i * block:(i + 1) * block]
        data, flag = compress(i, raw)
        if len(data) >= block:      # like real writers: incompressible blocks are stored
            data, flag = raw, (0 if ver == 2 and magic == b"CISO" else 0x80000000)
        index.append((pos >> align) | flag)
        pad = (-len(data)) % (1 << align)
        chunks.append(data + b"\0" * pad)
        pos += len(data) + pad
    index.append(pos >> align)
    with open(path, "wb") as f:
        f.write(magic + struct.pack("<IQIBBH", 24, len(src), block, ver, align, 0))
        f.write(b"".join(struct.pack("<I", x) for x in index))
        f.write(b"\0" * (head_len - 24 - 4 * len(index)))
        f.write(b"".join(chunks))

def deflate(raw):
    c = zlib.compressobj(9, zlib.DEFLATED, -15)
    return c.compress(raw) + c.flush()

def cso1(i, raw):          # every 3rd block stored uncompressed (high bit = plain)
    return (raw, 0x80000000) if i % 3 == 2 else (deflate(raw), 0)

def cso2(i, raw):          # v2: high bit = LZ4, otherwise deflate
    return (lz4.block.compress(raw, store_size=False), 0x80000000) if i % 2 else (deflate(raw), 0)

def zso(i, raw):           # ZSO: high bit = plain, otherwise LZ4
    return (raw, 0x80000000) if i % 4 == 3 else (lz4.block.compress(raw, mode="high_compression", store_size=False), 0)

src = open(sys.argv[1], "rb").read()
out = sys.argv[2]
os.makedirs(out, exist_ok=True)
write(os.path.join(out, "v1.cso"), b"CISO", 1, src, 2048, cso1)
write(os.path.join(out, "v1-align2.cso"), b"CISO", 1, src, 2048, cso1, align=2)
write(os.path.join(out, "v2.cso"), b"CISO", 2, src, 2048, cso2)
write(os.path.join(out, "game.zso"), b"ZISO", 1, src, 2048, zso)
print("ok")
