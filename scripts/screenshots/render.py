# render.py IN.ans OUT.png [WIDTH]
#     Draws a tmux "capture-pane -e" dump as a terminal window. With WIDTH the
#     image is scaled to that many pixels and reduced to 256 colours, as the
#     README's screenshots are.
# render.py --join OUT.png A.png B.png …
#     Puts already rendered windows side by side (docs/screenshots/phone.png).
# render.py --slices OUT.png theme-A.png theme-B.png …
#     One window cut into vertical slices, each from the next picture of the
#     same screen, named after the file in the title bar (themes.png).
#
# Needs Pillow, fontTools and the DejaVu and Noto symbol fonts.
import glob, os, re, sys, unicodedata
from PIL import Image, ImageDraw, ImageFont
from fontTools.ttLib import TTFont

def font(name):
    """Finds a font file by name wherever the distribution keeps it
    (/usr/share/fonts/TTF on Arch, .../truetype/dejavu on Debian), or
    under FONT_DIR."""
    roots = [os.environ.get("FONT_DIR", ""), "/usr/share/fonts", os.path.expanduser("~/.local/share/fonts")]
    for root in filter(None, roots):
        hits = glob.glob(os.path.join(root, "**", name), recursive=True)
        if hits:
            return sorted(hits)[0]
    sys.exit(f"render.py: can't find the font {name}; install it or set FONT_DIR")

SIZE = 26
FONTS = [font("DejaVuSansMono.ttf"), font("DejaVuSansMono-Bold.ttf")]
FALLBACK = [font("DejaVuSans.ttf"), font("NotoSansSymbols2-Regular.ttf"), font("NotoSansMath-Regular.ttf")]
EMOJI = font("NotoColorEmoji.ttf")  # a bitmap font: drawn at 109 px, then scaled
BG, FG = (21, 19, 30), (220, 216, 235)
if os.environ.get("BG"):  # a theme's own background, for themes.png
    BG = tuple(bytes.fromhex(os.environ["BG"].lstrip("#")))

reg = ImageFont.truetype(FONTS[0], SIZE)
bold = ImageFont.truetype(FONTS[1], SIZE)
cmap = TTFont(FONTS[0]).getBestCmap()
fb = [(ImageFont.truetype(p, SIZE), TTFont(p).getBestCmap()) for p in FALLBACK]
asc, desc = reg.getmetrics()
CW, CH = round(reg.getlength("M")), asc + desc + 2

def font_for(ch, b):
    if ord(ch) in cmap:
        return bold if b else reg
    for f, cm in fb:
        if ord(ch) in cm:
            return f
    return reg

def draw_cell(d, ch, px, py, fg):
    """Draws block and box characters as shapes so neighbours join up."""
    mx, my = px + CW // 2, py + CH // 2
    r, b = px + CW - 1, py + CH - 1
    blocks = {"█": (px, py, r, b), "▀": (px, py, r, my - 1), "▄": (px, my, r, b), "▌": (px, py, mx - 1, b)}
    if ch in blocks:
        d.rectangle(blocks[ch], fill=fg); return True
    # box drawing: which of up, down, left, right the line reaches
    arms = {"│": "ud", "─": "lr", "╭": "dr", "╮": "dl", "╰": "ur", "╯": "ul", "┬": "dlr", "┴": "ulr", "├": "udr", "┤": "udl", "┼": "udlr"}
    if ch not in arms:
        return False
    a = arms[ch]
    if "u" in a: d.rectangle([mx - 1, py, mx, my], fill=fg)
    if "d" in a: d.rectangle([mx - 1, my, mx, b], fill=fg)
    if "l" in a: d.rectangle([px, my - 1, mx, my], fill=fg)
    if "r" in a: d.rectangle([mx, my - 1, r, my], fill=fg)
    return True

def draw_emoji(img, ch, px, py):
    e = Image.new("RGBA", (160, 160))
    ImageDraw.Draw(e).text((0, 0), ch, font=ImageFont.truetype(EMOJI, 109), embedded_color=True)
    e = e.crop(e.getbbox())
    h = CH - 6
    e = e.resize((max(1, round(e.width * h / e.height)), h), Image.LANCZOS)
    img.paste(e, (px + (2 * CW - e.width) // 2, py + 3), e)

def finish(img, out, width=None):
    if width:
        img = img.resize((width, round(img.height * width / img.width)), Image.LANCZOS)
    img = img.convert("RGB").quantize(256, dither=Image.Dither.NONE)
    img.save(out, optimize=True)
    print(out, img.size)

if sys.argv[1] == "--join":
    parts = [Image.open(p).convert("RGB") for p in sys.argv[3:]]
    GAP = 40
    out = Image.new("RGB", (sum(p.width for p in parts) + GAP * (len(parts) - 1), max(p.height for p in parts)), (13, 12, 19))
    x = 0
    for p in parts:
        out.paste(p, (x, 0)); x += p.width + GAP
    finish(out, sys.argv[2], 1400)  # as wide as the other shots
    sys.exit()

if sys.argv[1] == "--slices":
    paths = sys.argv[3:]
    parts = [Image.open(p).convert("RGB") for p in paths]
    out = parts[0].copy()
    d = ImageDraw.Draw(out)
    x0, x1 = 40, out.width - 40  # the window, inside the margin render draws
    step = (x1 - x0) / len(parts)
    for i, (p, part) in enumerate(zip(paths, parts)):
        a, b = round(x0 + i * step), round(x0 + (i + 1) * step)
        out.paste(part.crop((a, 0, b, out.height)), (a, 0))
        if i:
            d.line([(a, 40 + 54), (a, out.height - 41)], fill=(13, 12, 19), width=3)
        name = os.path.basename(p)[:-4].removeprefix("theme-")
        w = bold.getlength(name)
        cx = (a + b) / 2 if i else max((a + b) / 2, 150 + w / 2)  # clear of the window's dots
        d.rounded_rectangle([cx - w / 2 - 14, 52, cx + w / 2 + 14, 52 + CH + 4], 12, fill=(58, 54, 80))
        d.text((cx - w / 2, 54), name, font=bold, fill=FG)
    finish(out, sys.argv[2], 1400)
    sys.exit()

lines = open(sys.argv[1], encoding="utf-8").read().rstrip("\n").split("\n")
cells = []  # rows of (ch, fg, bg, bold)
sgr = re.compile(r"\x1b\[([0-9;]*)m")
for line in lines:
    fg, bg, b = FG, None, False
    row, i = [], 0
    while i < len(line):
        m = sgr.match(line, i)
        if m:
            ps = [int(x) if x else 0 for x in m.group(1).split(";")] if m.group(1) else [0]
            j = 0
            while j < len(ps):
                p = ps[j]
                if p == 0: fg, bg, b = FG, None, False
                elif p == 1: b = True
                elif p == 22: b = False
                elif p == 39: fg = FG
                elif p == 49: bg = None
                elif p in (38, 48) and j + 4 < len(ps) + 0 and ps[j+1] == 2:
                    c = tuple(ps[j+2:j+5])
                    if p == 38: fg = c
                    else: bg = c
                    j += 4
                elif p in (38, 48) and ps[j+1] == 5:
                    j += 2
                j += 1
            i = m.end(); continue
        if line[i] == "\x1b":  # other escapes
            m2 = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]").match(line, i)
            i = m2.end() if m2 else i + 1; continue
        row.append((line[i], fg, bg, b))
        if unicodedata.east_asian_width(line[i]) in "WF":
            row.append(("", fg, bg, b))  # it takes two cells, as in the terminal
        i += 1
    cells.append(row)

cols = max(len(r) for r in cells); rows = len(cells)
PAD, BAR = 36, 54
W, H = cols * CW + 2 * PAD, rows * CH + 2 * PAD + BAR
img = Image.new("RGBA", (W + 80, H + 80), (0, 0, 0, 0))
d = ImageDraw.Draw(img)
ox, oy = 40, 40
d.rounded_rectangle([ox, oy, ox + W, oy + H], 18, fill=BG + (255,), outline=(58, 54, 80, 255), width=2)
for k, c in enumerate([(255, 95, 86), (255, 189, 46), (39, 201, 63)]):
    x = ox + 28 + k * 30
    d.ellipse([x, oy + 20, x + 16, oy + 36], fill=c)
tx, ty = ox + PAD, oy + BAR + PAD // 2
for y, row in enumerate(cells):
    for x, (ch, fg, bg, b) in enumerate(row):
        px, py = tx + x * CW, ty + y * CH
        if bg: d.rectangle([px, py, px + CW - 1, py + CH - 1], fill=bg)
        if ch not in (" ", ""):
            # block and box characters stretch over the whole cell so they join
            if draw_cell(d, ch, px, py, fg): continue
            if ch and font_for(ch, b) is reg and ord(ch) not in cmap and unicodedata.east_asian_width(ch) == "W":
                draw_emoji(img, ch, px, py); continue
            d.text((px, py + 1), ch, font=font_for(ch, b), fill=fg)
bg = Image.new("RGBA", img.size, (0, 0, 0, 255))
finish(Image.alpha_composite(bg, img), sys.argv[2], int(sys.argv[3]) if len(sys.argv) > 3 else None)
