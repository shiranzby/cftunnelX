#!/usr/bin/env python3
"""Create a Unix `ar` archive (the format used by IPK/deb packages).

Why this exists: building an IPK needs `ar`, which is part of binutils and is
not guaranteed to be present (it is missing on plain Windows dev machines and
on minimal CI images). This pure-Python implementation removes that dependency
so `scripts/pack-openwrt-ipk.sh` works everywhere.

Usage:
    python ar_create.py OUTPUT.ar MEMBER [MEMBER ...]

Member file names are stored as their basename, matching the "common" ar
format used by opkg/dpkg (name padded to 16 bytes with a trailing '/').
"""

import os
import sys

GLOBAL_HEADER = b"!<arch>\n"


def member_header(name: str, size: int) -> bytes:
    # Each field is fixed width and space padded (name uses '/')
    field = lambda value, width: str(value).ljust(width)  # noqa: E731
    header = (
        (name + "/")[:16].ljust(16)
        + field(0, 12)        # mtime
        + field(0, 6)         # uid
        + field(0, 6)         # gid
        + field("100644", 8)  # mode
        + field(size, 10)     # size
        + "`\n"
    )
    return header.encode("ascii")


def build(output: str, members: list[str]) -> None:
    with open(output, "wb") as out:
        out.write(GLOBAL_HEADER)
        for path in members:
            with open(path, "rb") as fh:
                data = fh.read()
            out.write(member_header(os.path.basename(path), len(data)))
            out.write(data)
            # Members are 2-byte aligned
            if len(data) % 2:
                out.write(b"\n")


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(__doc__)
        return 2
    output, members = argv[0], argv[1:]
    for path in members:
        if not os.path.isfile(path):
            print("missing member: %s" % path, file=sys.stderr)
            return 1
    build(output, members)
    print("created %s (%d bytes)" % (output, os.path.getsize(output)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
