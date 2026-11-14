import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  // no next/image anywhere (plain <img> of signed links), so the optimizer and sharp's ~18 MB of libraries stay out
  // of the standalone image
  images: { unoptimized: true },
  outputFileTracingExcludes: { "*": ["node_modules/sharp/**", "node_modules/@img/**"] },
  agentRules: false, // next dev would write AGENTS.md; the repo has CLAUDE.md
  allowedDevOrigins: ["127.0.0.1"], // make dev and the e2e suite use this host; Next 16 blocks dev assets otherwise
  // proxy.ts (token refresh) sees every /api request; Next cuts its body at 10 MB by default, which breaks job
  // uploads (several photos) and gallery zips. Match the largest body the API takes: a 500 MB zip plus form fields.
  // ponytail: proxy.ts buffers up to this much per request; skip /api uploads in the matcher if memory matters.
  experimental: { proxyClientMaxBodySize: "520mb" },
};

export default nextConfig;
