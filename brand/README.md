# dotshift brand kit

The dotshift logo is the **Route** mark: one dot comes in, three paths go out, and the path that is doing the work lights up blue. The wordmark is Manrope ExtraBold with the dot on the "i" shifted slightly to the right and colored blue.

All letters in these files are outlined into shapes, so nothing depends on a font being installed.

## What's here

| Folder | Files | Use it for |
|---|---|---|
| `svg/` | `dotshift-logo-*` | Main horizontal logo (mark + name). First choice almost everywhere. |
| `svg/` | `dotshift-logo-stacked-*` | Mark above the name, for square-ish spaces. |
| `svg/` | `dotshift-mark-*` | The mark alone (64×64 grid), for avatars, small spaces, CLI docs. |
| `svg/` | `dotshift-wordmark-*` | The name alone, when the mark already appears nearby. |
| `svg/` | `dotshift-app-icon.svg`, `dotshift-app-icon-square.svg` | Navy app icon, rounded and full-bleed (iOS and Android round the corners themselves). |
| `png/` | Same logos as PNG at 1x and 2x, marks at 512 and 1024, app icon at 512 and 1024 | Slides, docs and anywhere SVG isn't accepted. Transparent backgrounds. |
| `favicon/` | `favicon.ico` (16/32/48), `favicon.svg`, `favicon-16/32.png`, `apple-touch-icon.png`, `icon-192/512.png`, `site.webmanifest` | Websites and docs sites. |
| `social/` | `dotshift-social-card.png` / `.svg` (1200×630) | GitHub social preview, link previews, OG image. |

Each logo comes in four colorways:

- `light`: for white or light backgrounds.
- `dark`: for navy, black or dark-mode backgrounds.
- `mono-black` / `mono-white`: one color only, for print, stamps, or places that can't show color. In mono, the two inactive dots become rings so the "one path is chosen" idea still reads.

## Colors

| Name | Hex | Where |
|---|---|---|
| Ink | `#131826` | Mark and name on light backgrounds; app icon background |
| Route blue | `#2F54EB` | The active dot and the "i" dot on light backgrounds |
| Idle grey | `#C4C9D4` | Inactive dots on light backgrounds |
| Paper | `#F2F4F8` | Mark and name on dark backgrounds |
| Route blue (dark) | `#7C98FF` | Active dot on dark backgrounds |
| Idle grey (dark) | `#4A5266` | Inactive dots on dark backgrounds |
| Night | `#0E1220` | Social card background |

## Type

- **Wordmark:** Manrope ExtraBold (800), tracking −3.5%, already outlined in the files.
- **Supporting text** (taglines, docs headings): Manrope Medium/SemiBold. Manrope is free on Google Fonts (SIL Open Font License).

## Usage

- Leave clear space around the logo at least the width of the big input dot on every side.
- Don't use the horizontal logo smaller than 96 px wide on screens; switch to the mark alone below that.
- Keep the active dot in the middle path, and keep it the only blue element in the mark.
- Don't recolor, stretch, rotate, add effects, or re-set the name in another font.
- Only one dot is ever blue: the active route in the mark and the shifted "i" dot in the name.

## Snippets

Website `<head>`:

```html
<link rel="icon" href="/favicon.ico" sizes="48x48">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="manifest" href="/site.webmanifest">
<meta property="og:image" content="/dotshift-social-card.png">
```

`favicon.svg` switches to the dark colors automatically when the browser is in dark mode.

README header that follows GitHub's light/dark theme:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand/svg/dotshift-logo-dark.svg">
  <img alt="dotshift" src="brand/svg/dotshift-logo-light.svg" height="56">
</picture>
```

For the GitHub social preview, upload `social/dotshift-social-card.png` under the repo's Settings > General > Social preview.
