# dotshift.dev

This optional marketing site runs independently of local coding and review.
It is not part of dotshift startup, its monitor, or the core build.

The dotshift marketing website, built with React, TypeScript, and Vinext. Product copy follows the parent repository's README. The interactive runner is an illustrative workflow, not a live task monitor.

## Develop

Requires Node.js 22.13 or newer.

```sh
npm ci
npm run dev
```

The local URL is http://localhost:5173. Main content and interactive components live in `app/page.tsx`; the responsive design lives in `app/globals.css`.

## Branding

The page uses the Offset logo (concept 1): five equal circles, with one shifted above and to the right of a two-by-two grid. Masters, icons, social artwork, and a brand guide live in the parent `brand/` directory. Runtime copies live in `public/brand/` and `public/`. The primary logo is monochrome; secondary artwork accents only the shifted dot. Colors are Ink `#131826`, Shift blue `#2F54EB`, Paper `#F2F4F8`, and dark-background accent `#7C98FF`. Manrope and IBM Plex Mono are self-hosted in `public/fonts/`, with their SIL Open Font License files.

Run `make brand` from the repository root to synchronize the marketing and dashboard assets, font files, and shared color tokens. `make brand-check` detects drift without changing files. The site uses the Offset repeat pattern behind its workflow preview.

## Verify and build

```sh
npx tsc --noEmit
npm run build
```

The build produces a Cloudflare Worker in `dist/server` and browser assets in `dist/client`. Keep the Sites Vite plugin and `.openai/hosting.json` together when deploying through Sites.

## Domain

Canonical URLs, the sitemap, and social metadata target `https://dotshift.dev`. Setting these does not configure DNS. The custom domain must be connected to the chosen hosting provider before launch.

## Content and interactions

- Three manually selectable workflow stages, with keyboard-accessible tabs.
- Subscription/API setup tabs with copyable commands and clipboard feedback.
- Native FAQ disclosures and a mobile navigation menu.
- Reduced-motion support, visible focus indicators, and a skip link.
- Source and documentation links point to `https://github.com/gregwhitaker/dotshift`.

Subscription setup requires the official Codex and Claude Code CLIs. API setup requires Docker and provider credentials. No accounts, analytics, waitlist submissions, or live agent calls are made by this website.
