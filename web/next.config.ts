import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Always on: this dashboard is only ever shipped with a real API behind /v1. The flag is read at build time, and the
  // release build has no GREYSHIP_API_URL, so deriving it from that left sign-in and device approval in mock mode.
  env: { NEXT_PUBLIC_API_ENABLED: "1" },
  devIndicators: false,
  // The installer runs the dashboard from this self-contained build (node server.js).
  output: "standalone",
  // No image optimizer: its native library is built for one CPU, and this bundle must run on any. Plain <img> and
  // SVG are all the dashboard uses.
  images: { unoptimized: true },
  outputFileTracingExcludes: { "*": ["node_modules/@img/**", "node_modules/sharp/**"] },
  cacheComponents: true,
  partialPrefetching: true,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
};

export default nextConfig;
