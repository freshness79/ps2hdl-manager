/*
 * Batch modifier for the embedded hdl_dump (part of PS2 HDL Manager, GPL-2.0).
 *
 * Applies many game modifications in ONE process using hdl_dump's own functions
 * (hio_probe, apa_toc_read_ex, hdl_modify_game, apa_commit_ex): the disk is opened
 * and the partition table read once; hdl_modify_game keeps the in-memory table up
 * to date after every change, so following operations see the new partition ids.
 * Games are addressed by slice + start sector (known from the partition table),
 * so no "lookup by game name" scan of every game header is needed.
 *
 * stdin, one operation per line, tab-separated:
 *   F <slice> <sector> <mask>            set compatibility flags (bitmask 0..254)
 *   R <slice> <sector> <tmpname> <name>  rename via a temporary name; on failure
 *                                        of the second step the old name is restored
 *   Z <slice> <sector>                   zero the bytes after the partition id's NUL
 *                                        (repairs ids left dirty by old renames)
 * stdout, one line per operation:
 *   OK <n>  |  ERR <n> <retcode> <message>
 * Exit code: 0 if every operation succeeded, 1 otherwise, 100+ for setup errors.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#if defined(_WIN32)
#include <direct.h>
#define batch_chdir _chdir
#else
#include <unistd.h>
#define batch_chdir chdir
#endif

#include "config.h"
#include "retcodes.h"
#include "common.h"
#include "dict.h"
#include "hio.h"
#include "apa.h"
#include "hdl.h"

#define HDL_HEADER_OFFS 0x101000
#define SLICE_2_OFFS 0x10000000u

/* read the current game name from the HDL header */
static int read_game_name(hio_t *hio, int slice, u_int32_t start, char *name, size_t size)
{
    unsigned char hdr[1024];
    u_int32_t bytes = 0;
    u_int32_t sector = start + HDL_HEADER_OFFS / 512 + (u_int32_t)slice * SLICE_2_OFFS;
    int result = hio->read(hio, sector, 2, hdr, &bytes);
    if (result == RET_OK && bytes == sizeof(hdr)) {
        size_t n = 0;
        while (n + 1 < size && n < 0xa0 && hdr[8 + n] != 0) {
            name[n] = (char)hdr[8 + n];
            ++n;
        }
        name[n] = '\0';
        return RET_OK;
    }
    return result == RET_OK ? RET_ERR : result;
}

static char *next_field(char **p)
{
    char *start = *p, *tab;
    if (start == NULL)
        return NULL;
    tab = strchr(start, '\t');
    if (tab != NULL) {
        *tab = '\0';
        *p = tab + 1;
    } else
        *p = NULL;
    return start;
}

int hdl_batch_main(int argc, char **argv)
{
    dict_t *config;
    hio_t *hio = NULL;
    apa_toc_t *toc = NULL;
    char line[1024];
    int result, failures = 0, n = 0;

    if (argc < 2) {
        printf("FATAL\t%d\tusage: --embedded-hdl_batch device < ops\n", RET_ERR);
        return 100;
    }
    config = dict_alloc();
    if (config == NULL)
        return 100 + RET_NO_MEM;
    set_config_defaults(config);
    (void)dict_restore(config, get_config_file());

    result = hio_probe(config, argv[1], &hio);
    if (result != RET_OK || hio == NULL) {
        printf("FATAL\t%d\tcannot open %s\n", result, argv[1]);
        dict_free(config);
        return 100 + (result < 0 ? 0 : result);
    }
    result = apa_toc_read_ex(hio, &toc);
    if (result != RET_OK || toc == NULL) {
        printf("FATAL\t%d\tcannot read the partition table\n", result);
        (void)hio->close(hio);
        dict_free(config);
        return 100 + (result < 0 ? 0 : result);
    }

    while (fgets(line, sizeof(line), stdin) != NULL) {
        char *p = line, *op, *f_slice, *f_sector;
        int slice;
        u_int32_t sector;
        size_t len = strlen(line);
        while (len > 0 && (line[len - 1] == '\n' || line[len - 1] == '\r'))
            line[--len] = '\0';
        if (len == 0)
            continue;
        ++n;
        op = next_field(&p);
        f_slice = next_field(&p);
        f_sector = next_field(&p);
        if (op == NULL || f_slice == NULL || f_sector == NULL) {
            printf("ERR\t%d\t%d\tmalformed operation\n", n, RET_ERR);
            ++failures;
            fflush(stdout);
            continue;
        }
        slice = atoi(f_slice);
        sector = (u_int32_t)strtoul(f_sector, NULL, 10);

        if (strcmp(op, "F") == 0) {
            char *f_mask = next_field(&p);
            int mask = f_mask != NULL ? atoi(f_mask) : -1;
            if (mask < 0 || mask >= (int)COMPAT_FLAGS_INVALID)
                result = RET_ERR;
            else
                result = hdl_modify_game(hio, toc, slice, sector, NULL,
                                         (compat_flags_t)mask, 0, -1);
        } else if (strcmp(op, "R") == 0) {
            char *tmp = next_field(&p);
            char *name = next_field(&p);
            char old[0xa0 + 1];
            if (tmp == NULL || name == NULL || *tmp == '\0' || *name == '\0')
                result = RET_ERR;
            else {
                result = read_game_name(hio, slice, sector, old, sizeof(old));
                if (result == RET_OK)
                    result = hdl_modify_game(hio, toc, slice, sector, tmp,
                                             COMPAT_FLAGS_INVALID, 0, -1);
                if (result == RET_OK) {
                    result = hdl_modify_game(hio, toc, slice, sector, name,
                                             COMPAT_FLAGS_INVALID, 0, -1);
                    if (result != RET_OK) { /* put the original name back */
                        int restore = hdl_modify_game(hio, toc, slice, sector, old,
                                                      COMPAT_FLAGS_INVALID, 0, -1);
                        if (restore != RET_OK)
                            printf("WARN\t%d\t%d\tgame left with temporary name %s\n", n, restore, tmp);
                    }
                }
            }
        } else if (strcmp(op, "Z") == 0) {
            apa_slice_t *sl = slice >= 0 && slice < 2 ? toc->slice + slice : NULL;
            u_int32_t i;
            result = RET_NOT_FOUND;
            for (i = 0; sl != NULL && i < sl->part_count; ++i) {
                apa_partition_t *part = sl->parts + i;
                if (get_u32(&part->header.start) == sector) {
                    size_t len = strnlen(part->header.id, sizeof(part->header.id));
                    if (len < sizeof(part->header.id))
                        memset(part->header.id + len, 0, sizeof(part->header.id) - len);
                    set_u32(&part->header.checksum, apa_partition_checksum(&part->header));
                    part->modified = 1;
                    result = apa_commit_ex(hio, toc);
                    break;
                }
            }
        } else if (strcmp(op, "H") == 0) {
            /* rewrite the HDD-OSD header parts (icon.sys, list.ico, ...) from the
             * files in dir; parts without a file there are preserved as they are */
            char *dir = next_field(&p);
            if (dir == NULL || *dir == '\0' || batch_chdir(dir) != 0)
                result = RET_ERR;
            else {
                /* its "Successfully read ..." lines are ignored by the reader */
                result = hdd_inject_header(hio, toc, slice, sector);
            }
        } else
            result = RET_ERR;

        if (result == RET_OK)
            printf("OK\t%d\n", n);
        else {
            printf("ERR\t%d\t%d\t%s\n", n, result,
                   result == RET_PART_EXISTS ? "a partition with this name already exists" :
                   result == RET_NOT_FOUND   ? "game partition not found" :
                                               "operation failed");
            ++failures;
        }
        fflush(stdout);
    }

    apa_toc_free(toc);
    (void)hio->close(hio);
    dict_free(config);
    return failures ? 1 : 0;
}
