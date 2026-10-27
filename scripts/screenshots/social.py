# social.py OUT.png
#     The 1280×640 link preview (GitHub's "Social preview", og:image): the
#     name and pitch on the left, the Server screenshot on the right.
import sys
from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 1280, 640
BG, FG, MUTED = (21, 19, 30), (220, 216, 235), (140, 134, 160)
PURPLE, PINK, YELLOW = (157, 124, 252), (220, 122, 202), (250, 193, 101)
MONO = "/usr/share/fonts/TTF/DejaVuSansMono.ttf"
MONO_B = "/usr/share/fonts/TTF/DejaVuSansMono-Bold.ttf"

img = Image.new("RGB", (W, H), BG)

# a soft purple glow behind the screenshot
glow = Image.new("L", (W, H), 0)
ImageDraw.Draw(glow).ellipse((700, 40, 1500, 760), fill=110)
glow = glow.filter(ImageFilter.GaussianBlur(140))
img.paste(Image.new("RGB", (W, H), PURPLE), mask=glow)

# the screenshot's window (without the black around it), rounded, with a shadow
shot = Image.open("docs/screenshots/server.png").convert("RGB").crop((28, 28, 1373, 939))
shot = shot.resize((round(shot.width * 0.62), round(shot.height * 0.62)), Image.LANCZOS)
mask = Image.new("L", shot.size, 0)
ImageDraw.Draw(mask).rounded_rectangle((0, 0, shot.width - 1, shot.height - 1), 14, fill=255)
x, y = 600, 110
shadow = Image.new("L", (W, H), 0)
shadow.paste(mask, (x + 10, y + 24))
img.paste((5, 4, 10), mask=shadow.filter(ImageFilter.GaussianBlur(24)))
img.paste(shot, (x, y), mask)
ImageDraw.Draw(img).rounded_rectangle((x, y, x + shot.width - 1, y + shot.height - 1), 14, outline=(70, 60, 110), width=2)

d = ImageDraw.Draw(img)
f = lambda path, size: ImageFont.truetype(path, size)

# logo mark, as in the TUI's header
d.polygon([(70, 140), (118, 104), (118, 140)], fill=PURPLE)
d.text((136, 92), "interzone", font=f(MONO_B, 58), fill=PINK)

d.text((72, 200), "Matrix, deployed anywhere.", font=f(MONO_B, 30), fill=FG)
y = 256
for line in ["A terminal wizard for", "matrix-docker-ansible-deploy:", "pick a homeserver, bridges and", "bots, deploy it and run it.", "", "Keeps working through an", "internet blackout."]:
    d.text((72, y), line, font=f(MONO, 22), fill=MUTED)
    y += 32

# chips
x, y = 72, 520
for text, fg in [("BLACKOUT-READY", YELLOW), ("TUI", PURPLE), ("Go", PURPLE)]:
    font = f(MONO_B, 20)
    w = round(font.getlength(text))
    if fg == YELLOW:
        d.rectangle((x, y, x + w + 24, y + 36), fill=YELLOW)
        d.text((x + 12, y + 6), text, font=font, fill=BG)
    else:
        d.rectangle((x, y, x + w + 24, y + 36), outline=fg, width=2)
        d.text((x + 12, y + 6), text, font=font, fill=fg)
    x += w + 40

img.save(sys.argv[1], optimize=True)
print(sys.argv[1], img.size)
