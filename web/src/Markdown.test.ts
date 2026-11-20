import test from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Markdown } from "./Markdown.ts";

test("reports are readable without executing HTML or fetching report images", () => {
  const html = renderToStaticMarkup(createElement(Markdown, { children: `Search is **working**.

- Passed \`npm test\`
- Ready for review

<script>alert('secret')</script>
<img src="https://example.com/track" onerror="alert(1)">

![external image](https://example.com/tracker)

[unsafe](javascript:alert%281%29)

[documentation](https://example.com/docs)` }));
  assert.match(html, /<strong>working<\/strong>/);
  assert.match(html, /<ul>/);
  assert.match(html, /<code>npm test<\/code>/);
  assert.doesNotMatch(html, /<script|<img|onerror=|javascript:|src=/i);
  assert.match(html, /href="https:\/\/example.com\/docs"[^>]*rel="noopener noreferrer"/);
});
