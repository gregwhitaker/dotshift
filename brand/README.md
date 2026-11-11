# dotshift brand kit: Offset

The logo is **Offset**, the selected concept 1: five equal circles, with four in a two-by-two grid and the fifth shifted above and to the right. It expresses steady work moving forward. The primary logo is monochrome; a secondary version colors only the offset circle blue.

The lowercase wordmark uses the existing outlined Manrope ExtraBold lettering with a centered, monochrome dot over the i. All production SVG lettering is outlined and needs no installed font.

## Start here

- `guide/dotshift-brand-guide.pdf`: five-page identity, color, typography, voice, and usage guide.
- `previews/dotshift-offset-production-board.png`: exact production assets in one overview.
- `previews/dotshift-offset-brand-board.png`: the built-in imagegen concept presentation. Use production SVGs for exact geometry and colors.
- `svg/dotshift-logo-light.svg`: default logo for a light background.
- `svg/dotshift-logo-dark.svg`: default logo for a dark background.

## Assets

| Folder | Contents | Use |
| --- | --- | --- |
| `svg/` | Horizontal and stacked logos, standalone marks and wordmarks | Websites, apps, print, editable artwork |
| `png/` | Transparent horizontal logos at 1x, 2x, and 4x; marks at 512/1024; stacked logos; app icons | Documents, presentations, image-based applications |
| `favicon/` | Theme-aware SVG, ICO with 16/32/48 frames, PNG favicons, Apple touch icon, 192/512 icons, manifest | Browser and installed web app icons |
| `social/` | 1200 x 630 artwork in SVG and PNG, plus 2x PNG | Link previews and GitHub social preview |
| `patterns/` | Repeat pattern in SVG and PNG | Supporting graphic backgrounds |
| `tokens/` | Exact palette and geometry in JSON; CSS custom properties | Design and application handoff |
| `fonts/` | Manrope 400/600/800 and IBM Plex Mono 400; original license files | Supporting typography |
| `source/` | Vector master, generation scripts, raster export manifest, imagegen prompt | Reproducible source and provenance |

Variants ending in `light` are intended for light backgrounds; `dark` variants are for dark backgrounds. `accent-light` and `accent-dark` color only the offset circle. `mono-black` and `mono-white` are true one-color versions. The app icon's four base circles are white, with a lighter blue offset circle on Ink. The alternate blue app icon uses five white circles.

## Color

| Token | Hex | Role |
| --- | --- | --- |
| Ink | `#131826` | Primary logo, headlines, dark surfaces |
| Shift blue | `#2F54EB` | Accent on light backgrounds |
| Paper | `#F2F4F8` | Light surfaces and reverse logo |
| Night | `#0E1220` | Dark social artwork |
| Shift blue dark | `#7C98FF` | Accent on dark backgrounds |
| Slate | `#4A5266` | Supporting text |
| Line | `#DFE4EB` | Borders and dividers |

## Typography and voice

Manrope 800 is the headline and wordmark face; use 600 for subheads and 400 for body copy. IBM Plex Mono 400 is for commands and technical labels. Font licenses are included beside the font files.

Primary line: **Big ideas. Meet follow-through.**

Supporting line: **Your machine. Your models. Your branch.**

Describe concrete outcomes: code, test, fix, review. Keep the tone clear, capable, and grounded. Avoid claims of perfection or guaranteed outcomes. Write the product name as `dotshift`, without an extra terminal period as part of the logo.

## Geometry and use

- The mark uses a 64 x 64 viewBox and five circles of radius 9.
- Centers are `(13, 31)`, `(33, 31)`, `(13, 51)`, `(33, 51)`, and `(49, 15)`.
- Keep at least one dot diameter of clear space beyond the visible artwork.
- Use the horizontal logo at 144 px wide or larger. Prefer a 24 px mark; the supplied 16 px favicon is optimized for browser use.
- Keep all five circles equal, solid, and separated. Never add branches, shrink the offset dot, rotate the mark, or stretch it.
- Keep the wordmark's i dot centered and the same color as the lettering.
- Use the square, full-bleed icon for platform masks; use the rounded icon when the image itself must supply its corners.

## Local integration

The root README references these masters. `make brand` synchronizes matching SVGs, icons, fonts, and color tokens into the marketing site and local dashboard; `make brand-check` detects drift. Both web surfaces use Manrope and IBM Plex Mono with the same palette. The macOS menu bar draws the five-circle geometry as a monochrome template, while its app bundle uses the full-color app icon. `make menubar` rebuilds the CLI, dashboard, app icon, and native bundle together. Running services and menu apps need to be relaunched to load a new build; the marketing site is published separately through Sites.

The previous Route kit remains available in Git history.

## Rebuilding

The source scripts require Python with `fonttools` and `reportlab`, and Node with `sharp`. Fonts are bundled. From the repository root:

```sh
python3 brand/source/build-assets.py
node brand/source/render-assets.mjs
python3 brand/source/build-guide.py
```

The generated guide is also written to `output/pdf/dotshift-brand-guide.pdf`. Raster exports preserve transparency. Run `make brand` after regenerating the masters.

The generated concept board used the built-in imagegen tool; its exact prompt is in `source/imagegen-prompt.txt`. The production assets are constructed from native vector geometry and the existing outlined wordmark.
