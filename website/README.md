# dotshift.dev

The dotshift marketing website, built with React, TypeScript, and Vinext. Product copy follows the parent repository's README. The interactive runner is an illustrative workflow, not a live task monitor.

## Develop

Requires Node.js 22.13 or newer.

```sh
npm ci
npm run dev
```

The local URL is http://localhost:5173. Main content and interactive components live in `app/page.tsx`; the responsive design lives in `app/globals.css`.

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
