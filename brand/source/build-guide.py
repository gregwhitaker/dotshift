#!/usr/bin/env python3
"""Create the portable Offset brand guide using the bundled assets and fonts."""
from pathlib import Path
import shutil
from reportlab.pdfgen import canvas
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.colors import HexColor
from reportlab.lib.utils import simpleSplit

B = Path(__file__).resolve().parents[1]
ROOT = B.parent
OUT = ROOT / "output/pdf/dotshift-brand-guide.pdf"
OUT.parent.mkdir(parents=True, exist_ok=True)
W, H = 864, 576
INK, BLUE, PAPER, NIGHT, LIGHT_BLUE, SLATE, LINE = "#131826", "#2F54EB", "#F2F4F8", "#0E1220", "#7C98FF", "#4A5266", "#DFE4EB"
for name, file in [("Regular", "manrope-400"), ("Semi", "manrope-600"), ("Bold", "manrope-800"), ("Mono", "ibm-plex-mono-400")]:
    pdfmetrics.registerFont(TTFont(name, str(B / "fonts" / f"{file}.ttf")))
c = canvas.Canvas(str(OUT), pagesize=(W, H))
c.setTitle("dotshift - Offset brand guide")
c.setAuthor("dotshift")
c.setSubject("Concept 1: Offset. Identity, colors, typography, voice, and assets.")

def rect(x, y, w, h, color, radius=0, stroke=None):
    c.setFillColor(HexColor(color))
    if stroke:
        c.setStrokeColor(HexColor(stroke)); c.setLineWidth(.7)
    c.roundRect(x, H-y-h, w, h, radius, stroke=1 if stroke else 0, fill=1)

def line(value, x, top, size=12, color=INK, font="Regular"):
    c.setFillColor(HexColor(color)); c.setFont(font, size)
    c.drawString(x, H-top-size, value)

def para(value, x, top, width, size=12, color=SLATE, font="Regular", leading=None):
    leading = leading or size*1.55
    for i, row in enumerate(simpleSplit(value, font, size, width)):
        line(row, x, top+i*leading, size, color, font)

def image(name, x, y, width, height):
    c.drawImage(str(B/name), x, H-y-height, width=width, height=height, mask="auto", preserveAspectRatio=True)

def mark(x, y, scale, ink=INK, accent=None):
    for i, (cx, cy) in enumerate([(13,31),(33,31),(13,51),(33,51),(49,15)]):
        c.setFillColor(HexColor(accent if i==4 and accent else ink))
        c.circle(x+cx*scale, H-y-cy*scale, 9*scale, stroke=0, fill=1)

def page(number, section, dark=False):
    rect(0,0,W,H,NIGHT if dark else PAPER)
    color=LIGHT_BLUE if dark else SLATE
    line("DOTSHIFT / OFFSET", 44, 26, 10, color, "Mono")
    line(section.upper(), 578, 26, 10, color, "Mono")
    line("IDENTITY GUIDE  /  NOVEMBER 2026", 44, 542, 8, color, "Mono")
    line(f"{number:02d} / 05", 772, 542, 8, color, "Mono")

def title(value, subtitle=None):
    line(value,44,74,32,INK,"Bold")
    if subtitle: para(subtitle,44,124,760,12)

# 1 - Identity
page(1,"Brand foundation",True)
image("png/dotshift-logo-dark@4x.png",44,99,427,82)
line("Big ideas.",44,246,49,PAPER,"Bold")
line("Meet follow-through.",44,304,42,LIGHT_BLUE,"Bold")
para("Local coding and review that keeps work moving.",44,394,510,17,PAPER)
para("Your machine. Your models. Your branch.",44,447,510,13,LIGHT_BLUE)
mark(620,200,2.8,PAPER,LIGHT_BLUE)
c.showPage()

# 2 - Mark and rules
page(2,"Logo system")
title("One dot moves forward.","Four steady dots and one deliberate shift. Keep the silhouette consistent everywhere.")
rect(44,173,290,248,"#FFFFFF",16)
mark(108,216,2.35)
c.setStrokeColor(HexColor(LINE)); c.setLineWidth(.7); c.setDash(3,3)
c.rect(75.1,H-187.8-211.5,211.5,211.5,stroke=1,fill=0); c.setDash()
line("FIVE EQUAL CIRCLES",80,405,10,SLATE,"Mono")
line("Geometry",374,174,18,INK,"Bold")
para("64 x 64 unit master. Every circle has a 9-unit radius. Four centers form a 20-unit grid; the fifth sits at (49, 15).",374,205,430)
line("Space and size",374,283,18,INK,"Bold")
para("Leave at least one dot diameter of clear space around the visible logo. Use the horizontal logo at 144 px wide or larger. Prefer a 24 px mark; use the supplied favicon at 16 px.",374,315,430)
rect(44,443,371,76,"#FFFFFF",12)
image("png/dotshift-logo-light@2x.png",75,461,254,49)
rect(435,443,385,76,INK,12)
image("png/dotshift-logo-dark@2x.png",469,461,254,49)
c.showPage()

# 3 - Color
page(3,"Color system")
title("Ink first. A little blue.","The primary logo is monochrome. Use the blue offset dot as a secondary signature.")
for i,(name,hx) in enumerate([("Ink",INK),("Shift blue",BLUE),("Paper",PAPER),("Night",NIGHT)]):
    x=44+i*198
    rect(x,179,182,133,hx,14,LINE if hx==PAPER else None)
    line(name,x,326,16,INK,"Bold")
    line(hx,x,353,11,SLATE,"Mono")
rect(44,411,374,102,INK,12)
mark(61,429,1.07,PAPER,LIGHT_BLUE)
line("Dark-background accent",151,430,14,PAPER,"Semi")
line(LIGHT_BLUE,151,457,11,LIGHT_BLUE,"Mono")
line("Supporting colors",454,410,16,INK,"Bold")
para("Slate #4A5266 for secondary text. Line #DFE4EB for dividers. Use Shift blue on light surfaces and the lighter accent on dark surfaces.",454,442,355,11.5)
c.showPage()

# 4 - Type and language
page(4,"Type and voice")
title("Clear, capable, grounded.","Use confident language about work and evidence, with enough warmth to sound human.")
line("Manrope",44,185,49,INK,"Bold")
line("Aa Bb Cc 0123456789",44,256,23,INK,"Regular")
para("800 for headlines and the outlined wordmark. 600 for subheads. 400 for body copy. Keep the name lowercase: dotshift.",44,308,350)
line("IBM Plex Mono",44,407,23,INK,"Mono")
para("Use for commands, status labels, and compact technical details.",44,449,350,11.5)
rect(440,176,380,337,"#FFFFFF",14)
line("Core message",464,197,17,INK,"Bold")
para("Give dotshift a goal. It codes, runs tests, fixes failures, and gets a fresh review on your machine.",464,232,329,13,INK)
line("Preferred language",464,327,17,INK,"Bold")
para("Use concrete verbs: build, test, fix, review. Say what happened and what needs attention. Keep progress messages short.",464,360,329)
para("Avoid claims such as perfect, effortless, or guaranteed. A completed job is supported by tests and review.",464,446,329,11)
c.showPage()

# 5 - Applications and delivery
page(5,"Applications and files")
title("Ready to use.","Use SVG for interfaces and print. Use PNG where the destination needs a raster image.")
image("social/dotshift-social-card.png",44,177,482,253.05)
image("png/dotshift-app-icon-512.png",564,177,112,112)
image("png/dotshift-app-icon-blue-1024.png",700,177,112,112)
line("Use the supplied masters",564,314,17,INK,"Bold")
para("Keep all five dots equal. Keep the offset at the upper right. Never rotate, stretch, add branches, or shrink the fifth dot.",564,348,248,11.5)
line("Main logo",44,459,12,INK,"Semi")
line("svg/dotshift-logo-light.svg",44,482,9.5,SLATE,"Mono")
line("App + browser",330,459,12,INK,"Semi")
line("svg/ + png/ + favicon/",330,482,9.5,SLATE,"Mono")
line("Tokens + fonts",610,459,12,INK,"Semi")
line("tokens/ + fonts/",610,482,9.5,SLATE,"Mono")
c.showPage()
c.save()
shutil.copy2(OUT,B/"guide/dotshift-brand-guide.pdf")
print(OUT)
