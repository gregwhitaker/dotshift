#!/usr/bin/env python3
"""Build the Offset SVG masters. Requires fonttools; fonts are bundled locally."""
from pathlib import Path
import html
import json
import xml.etree.ElementTree as ET
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen

BRAND = Path(__file__).resolve().parents[1]
COLORS = {"ink": "#131826", "shift-blue": "#2F54EB", "paper": "#F2F4F8", "night": "#0E1220", "shift-blue-dark": "#7C98FF", "slate": "#4A5266", "line": "#DFE4EB"}
DOTS = [(13, 31), (33, 31), (13, 51), (33, 51), (49, 15)]
RADIUS = 9
NS = {"s": "http://www.w3.org/2000/svg"}
WORD_PATH = ET.parse(BRAND / "source/wordmark-outline.svg").getroot().find("s:path", NS).attrib["d"]
EXPORTS = []

def svg(body, width, height, title="dotshift Offset logo"):
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" width="{width}" height="{height}" role="img" aria-label="{html.escape(title)}"><title>{html.escape(title)}</title>{body}</svg>\n'

def save(name, body):
    p = BRAND / name
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(body)

def export(source, target, width, height=None):
    EXPORTS.append({"source": source, "target": target, "width": width, **({"height": height} if height else {})})

def mark(ink, accent=None):
    return ''.join(f'<circle cx="{x}" cy="{y}" r="{RADIUS}" fill="{accent if i == 4 and accent else ink}"/>' for i, (x, y) in enumerate(DOTS))

def word(ink):
    return f'<path d="{WORD_PATH}" fill="{ink}"/><circle cx="5704" cy="-1350" r="146.88" fill="{ink}"/>'

def lockup(ink, accent=None):
    return mark(ink, accent) + f'<g transform="translate(75.34 55) scale(0.0332425)">{word(ink)}</g>'

FONT_CACHE = {}
def text(value, x, y, size, color, font="manrope-800", tracking=0, max_width=None):
    if font not in FONT_CACHE:
        FONT_CACHE[font] = TTFont(BRAND / "fonts" / f"{font}.ttf")
    f = FONT_CACHE[font]
    gs, cmap, metrics = f.getGlyphSet(), f.getBestCmap(), f["hmtx"].metrics
    upem = f["head"].unitsPerEm
    advance = sum(metrics[cmap[ord(c)]][0] for c in value) / upem * size + max(0, len(value)-1) * tracking
    if max_width and advance > max_width:
        size *= max_width / advance
        tracking *= max_width / advance
    scale, cursor, paths = size / upem, x, []
    for c in value:
        name = cmap[ord(c)]
        pen = SVGPathPen(gs)
        gs[name].draw(TransformPen(pen, (scale, 0, 0, -scale, cursor, y)))
        paths.append(pen.getCommands())
        cursor += metrics[name][0] * scale + tracking
    return f'<path d="{" ".join(paths)}" fill="{color}"/>'

def app_icon(square=False, blue=False):
    bg = COLORS["shift-blue"] if blue else COLORS["ink"]
    accent = "#FFFFFF" if blue else COLORS["shift-blue-dark"]
    return svg(f'<rect width="512" height="512" rx="{0 if square else 112}" fill="{bg}"/><g transform="translate(96 96) scale(5)">{mark("#FFFFFF", accent)}</g>', 512, 512, "dotshift Offset app icon")

def main():
    for folder in ["svg", "png", "favicon", "social", "patterns", "previews", "tokens"]:
        (BRAND / folder).mkdir(exist_ok=True)
    variants = {
        "light": (COLORS["ink"], None), "dark": (COLORS["paper"], None),
        "mono-black": ("#000000", None), "mono-white": ("#FFFFFF", None),
        "accent-light": (COLORS["ink"], COLORS["shift-blue"]),
        "accent-dark": (COLORS["paper"], COLORS["shift-blue-dark"]),
    }
    for name, (ink, accent) in variants.items():
        save(f"svg/dotshift-mark-{name}.svg", svg(mark(ink, accent), 64, 64))
        save(f"svg/dotshift-logo-{name}.svg", svg(lockup(ink, accent), 334, 64))
        stack = f'<g transform="translate(82 12) scale(1.5)">{mark(ink, accent)}</g><g transform="translate(19.71 182) scale(0.0299728)">{word(ink)}</g>'
        save(f"svg/dotshift-logo-stacked-{name}.svg", svg(stack, 260, 200))
        if not name.startswith("accent"):
            save(f"svg/dotshift-wordmark-{name}.svg", svg(f'<g transform="translate(-80 1496.88)">{word(ink)}</g>', 7340, 1526.88))
        for scale in [1, 2, 4]:
            suffix = "" if scale == 1 else f"@{scale}x"
            export(f"svg/dotshift-logo-{name}.svg", f"png/dotshift-logo-{name}{suffix}.png", 334 * scale)
        for size in [512, 1024]:
            export(f"svg/dotshift-mark-{name}.svg", f"png/dotshift-mark-{name}-{size}.png", size)
        export(f"svg/dotshift-logo-stacked-{name}.svg", f"png/dotshift-logo-stacked-{name}.png", 1040)

    save("svg/dotshift-app-icon.svg", app_icon())
    save("svg/dotshift-app-icon-square.svg", app_icon(square=True))
    save("svg/dotshift-app-icon-blue.svg", app_icon(blue=True))
    for size in [512, 1024]:
        export("svg/dotshift-app-icon.svg", f"png/dotshift-app-icon-{size}.png", size)
    export("svg/dotshift-app-icon-blue.svg", "png/dotshift-app-icon-blue-1024.png", 1024)

    favicon = '<style>.base{fill:#131826}.offset{fill:#2F54EB}@media(prefers-color-scheme:dark){.base{fill:#F2F4F8}.offset{fill:#7C98FF}}</style>'
    favicon += ''.join(f'<circle class="{"offset" if i==4 else "base"}" cx="{x}" cy="{y}" r="9"/>' for i, (x, y) in enumerate(DOTS))
    save("favicon/favicon.svg", svg(favicon, 64, 64))
    for size in [16, 32, 48]:
        export("svg/dotshift-mark-accent-light.svg", f"favicon/favicon-{size}.png", size)
    for size, name in [(180, "apple-touch-icon"), (192, "icon-192"), (512, "icon-512")]:
        export("svg/dotshift-app-icon-square.svg", f"favicon/{name}.png", size)
    manifest = {"name": "dotshift", "short_name": "dotshift", "icons": [{"src":"/icon-192.png","sizes":"192x192","type":"image/png"},{"src":"/icon-512.png","sizes":"512x512","type":"image/png","purpose":"any maskable"}], "theme_color":COLORS["ink"], "background_color":COLORS["ink"], "display":"standalone"}
    save("favicon/site.webmanifest", json.dumps(manifest, indent=2)+"\n")

    social = '<rect width="1200" height="630" fill="#0E1220"/>'
    social += f'<g transform="translate(64 56) scale(.9)">{lockup(COLORS["paper"], COLORS["shift-blue-dark"])}</g>'
    social += text("Big ideas.", 64, 267, 76, COLORS["paper"], tracking=-2)
    social += text("Meet follow-through.", 64, 353, 64, COLORS["shift-blue-dark"], tracking=-1.6, max_width=775)
    social += text("Your machine. Your models. Your branch.", 64, 482, 25, COLORS["paper"], "manrope-400")
    social += text("dotshift.dev", 64, 575, 17, COLORS["shift-blue-dark"], "ibm-plex-mono-400")
    social += f'<g transform="translate(905 246) scale(4)">{mark(COLORS["paper"], COLORS["shift-blue-dark"])}</g>'
    save("social/dotshift-social-card.svg", svg(social, 1200, 630, "dotshift - Big ideas. Meet follow-through."))
    export("social/dotshift-social-card.svg", "social/dotshift-social-card.png", 1200)
    export("social/dotshift-social-card.svg", "social/dotshift-social-card@2x.png", 2400)

    pattern = '<rect width="640" height="320" fill="#F2F4F8"/>'
    for y in range(30, 320, 90):
        for x in range(28, 640, 90):
            pattern += f'<g transform="translate({x} {y}) scale(.7)">{mark("#DFE4EB", COLORS["shift-blue"])}</g>'
    save("patterns/dotshift-offset-pattern.svg", svg(pattern, 640, 320, "dotshift Offset repeat pattern"))
    export("patterns/dotshift-offset-pattern.svg", "patterns/dotshift-offset-pattern.png", 1280)

    board = '<rect width="1600" height="1040" fill="#F2F4F8"/>'
    board += text("DOTSHIFT / OFFSET IDENTITY", 64, 61, 18, COLORS["slate"], "ibm-plex-mono-400", tracking=1)
    board += f'<g transform="translate(64 150) scale(2.7)">{lockup(COLORS["ink"])}</g>'
    board += '<rect x="1040" y="104" width="496" height="270" rx="28" fill="#131826"/>'
    board += f'<g transform="translate(1088 199) scale(1.2)">{lockup(COLORS["paper"])}</g>'
    board += text("01 / PRIMARY LOGO", 64, 390, 15, COLORS["slate"], "ibm-plex-mono-400")
    board += text("02 / APPLICATIONS", 64, 468, 15, COLORS["slate"], "ibm-plex-mono-400")
    board += '<rect x="64" y="500" width="208" height="208" rx="46" fill="#131826"/>'
    board += f'<g transform="translate(103 539) scale(2.03)">{mark(COLORS["paper"],COLORS["shift-blue-dark"])}</g>'
    board += '<rect x="296" y="500" width="208" height="208" rx="46" fill="#2F54EB"/>'
    board += f'<g transform="translate(335 539) scale(2.03)">{mark("#FFFFFF")}</g>'
    board += text("03 / COLOR", 570, 468, 15, COLORS["slate"], "ibm-plex-mono-400")
    for i, (label, key) in enumerate([("Ink", "ink"),("Shift blue", "shift-blue"),("Paper", "paper")]):
        y = 498+i*78
        board += f'<rect x="570" y="{y}" width="58" height="58" rx="12" fill="{COLORS[key]}" stroke="#DFE4EB"/>'
        board += text(label, 650, y+23, 20, COLORS["ink"], "manrope-600")
        board += text(COLORS[key], 650, y+49, 17, COLORS["slate"], "ibm-plex-mono-400")
    board += text("04 / TYPOGRAPHY", 1040, 468, 15, COLORS["slate"], "ibm-plex-mono-400")
    board += text("Manrope", 1040, 580, 80, COLORS["ink"], tracking=-3, max_width=480)
    board += text("IBM Plex Mono", 1040, 642, 29, COLORS["slate"], "ibm-plex-mono-400")
    board += text("Clear. Capable. Always moving.", 1040, 699, 22, COLORS["ink"], "manrope-400", max_width=480)
    board += '<rect x="64" y="786" width="1472" height="190" rx="28" fill="#0E1220"/>'
    board += text("Big ideas. Meet follow-through.", 112, 870, 50, COLORS["paper"], tracking=-1.5, max_width=1200)
    board += text("Your machine. Your models. Your branch.", 112, 929, 25, COLORS["shift-blue-dark"], "manrope-400")
    board += f'<g transform="translate(1346 829) scale(1.65)">{mark(COLORS["paper"],COLORS["shift-blue-dark"])}</g>'
    save("previews/dotshift-offset-production-board.svg", svg(board, 1600, 1040, "dotshift Offset production brand kit"))
    export("previews/dotshift-offset-production-board.svg", "previews/dotshift-offset-production-board.png", 1600)

    save("tokens/colors.json", json.dumps({"brand":"dotshift", "identity":"Offset", "colors":COLORS, "fonts":{"headings":"Manrope", "body":"Manrope", "code":"IBM Plex Mono"}, "mark":{"viewBox":[0,0,64,64],"radius":RADIUS,"centers":DOTS},"clearSpace":18}, indent=2)+"\n")
    css = ':root {\n'+''.join(f'  --dotshift-{k}: {v};\n' for k,v in COLORS.items())+'  --dotshift-font: "Manrope", sans-serif;\n  --dotshift-mono: "IBM Plex Mono", monospace;\n}\n'
    save("tokens/brand.css", css)
    save("source/raster-exports.json", json.dumps(EXPORTS, indent=2)+"\n")
    print(f"Built Offset SVG masters and {len(EXPORTS)} raster export definitions.")

if __name__ == "__main__":
    main()
