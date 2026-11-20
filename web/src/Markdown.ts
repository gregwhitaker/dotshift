import { createElement } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Agent reports are untrusted. Render nodes, never raw HTML or automatically
// fetched images. ReactMarkdown also rejects unsafe link URLs.
export function Markdown({ children }: { children: string }) {
  return createElement("div", { className: "report-markdown" }, createElement(ReactMarkdown, {
    children,
    skipHtml: true,
    remarkPlugins: [remarkGfm],
    components: {
      h1: ({ children }) => createElement("h3", null, children),
      h2: ({ children }) => createElement("h3", null, children),
      img: ({ alt }) => alt ? createElement("span", null, alt) : null,
      a: ({ href, children }) => createElement("a", { href, target: "_blank", rel: "noopener noreferrer" }, children),
    },
  }));
}
