import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The browser only needs to know whether a real API is behind /v1; its address stays server-side.
  env: { NEXT_PUBLIC_API_ENABLED: process.env.GREYSHIP_API_URL ? "1" : "" },
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
