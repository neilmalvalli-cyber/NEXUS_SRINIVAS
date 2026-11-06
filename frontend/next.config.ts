import type { NextConfig } from "next";

// The dashboard is a client-rendered tool that talks to the nexus-api adapter
// from the browser. Cache Components / partial prefetching are off: every page
// shows live data fetched with TanStack Query, so there is nothing to prerender.
const nextConfig: NextConfig = {
  cacheComponents: false,
  partialPrefetching: false,
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
