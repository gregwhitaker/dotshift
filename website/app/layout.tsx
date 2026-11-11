import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  metadataBase: new URL("https://dotshift.dev"),
  title: "dotshift — Big ideas. Meet follow-through.",
  description: "Give your coding agents a goal. dotshift builds, tests, and keeps fixing on your machine, then hands you a branch to review. Your agents. Your models. Your code.",
  alternates: { canonical: "https://dotshift.dev" },
  openGraph: {
    type: "website",
    url: "https://dotshift.dev",
    siteName: "dotshift",
    title: "Big ideas. Meet follow-through.",
    description: "The local coding engine that turns a goal into a tested, reviewed branch.",
    images: [{ url: "/dotshift-social-card.png", width: 1200, height: 630, alt: "dotshift — Big ideas. Meet follow-through." }],
  },
  twitter: { card: "summary_large_image", title: "dotshift — Big ideas. Meet follow-through.", description: "Your machine. Your models. Your branch.", images: ["/dotshift-social-card.png"] },
  manifest: "/site.webmanifest",
  icons: {
    icon: [{ url: "/favicon.svg?v=offset", type: "image/svg+xml" }, { url: "/favicon.ico?v=offset", sizes: "48x48" }],
    apple: "/apple-touch-icon.png?v=offset",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className="antialiased">{children}</body>
    </html>
  );
}
