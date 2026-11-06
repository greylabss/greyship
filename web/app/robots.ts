import type { MetadataRoute } from "next";

// A self-hosted dashboard is private: nothing here should be indexed.
export default function robots(): MetadataRoute.Robots {
  return { rules: [{ userAgent: "*", disallow: "/" }] };
}
