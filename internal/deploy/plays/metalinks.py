# Written by interzone. Usage: metalinks.py MIRROR FILE.repo
#
# Comments out metalink= and mirrorlist= in the sections of a dnf .repo file
# whose baseurl points at MIRROR, so dnf downloads from the mirror. Other
# sections keep their mirror lists: without a base URL they'd stop working.
# Prints "changed" if it changed the file.
import re
import sys

mirror, path = sys.argv[1], sys.argv[2]
with open(path) as f:
    lines = f.read().split("\n")

sections, current = [], []
for line in lines:
    if line.startswith("[") and current:
        sections.append(current)
        current = []
    current.append(line)
sections.append(current)

on_mirror = re.compile(r"\s*baseurl\s*=\s*" + re.escape(mirror) + r"(/|$)")
mirror_list = re.compile(r"(metalink|mirrorlist)\s*=")
out, changed = [], False
for section in sections:
    if any(on_mirror.match(line) for line in section):
        for i, line in enumerate(section):
            if mirror_list.match(line):
                section[i] = "#" + line
                changed = True
    out.extend(section)

if changed:
    with open(path, "w") as f:
        f.write("\n".join(out))
    print("changed")
