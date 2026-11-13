# Local patches to vendored sources

## hdl-dump 0.9.2 — hdl.c, hdl_modify_game()
`char part_prefix[3]` and `char game_id[11]` were filled with strncpy() without a
terminating NUL and then passed to hdl_pname(), which strcpy()s the prefix. Depending
on leftover stack contents the copy ran past the buffers and overwrote the start of the
HDL header (magic 0xdeadfeed) that is then written back to disk, making the game
invisible to HD Loader/OPL. Fixed by enlarging both buffers by one byte and
zero-initialising them. No other change.

## hdl-dump 0.9.2 — hdl.c, hdl_modify_game(): partition id padding
The new partition id was written with strcpy(), leaving the bytes of a longer previous
id after the terminating NUL (e.g. after a two-step rename through a temporary name).
The PS2 APA driver (and pfsshell) compares the whole 32-byte id, so such a partition
could no longer be opened or removed ("No such file or directory"), while hdl_dump
still listed it. Fixed by zero-filling the id field before copying. Partitions already
damaged this way are cleaned by the app ("Z" operation of the batch modifier) before
deleting them.
