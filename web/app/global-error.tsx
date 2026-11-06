"use client";

import "./globals.css";

export default function GlobalError({ retry }: { error: unknown; retry: () => void }) {
  return (
    <html lang="en">
      <body className="flex min-h-dvh flex-col items-center justify-center gap-4 px-5 text-center">
        <span className="font-mono text-13 text-dim">error</span>
        <h1 className="text-28 font-semibold">Greyship failed to load.</h1>
        <p className="text-muted">Your apps are unaffected.</p>
        <button type="button" onClick={retry} className="h-10 rounded-input bg-fg px-4 font-medium text-bg">
          Try again
        </button>
      </body>
    </html>
  );
}
