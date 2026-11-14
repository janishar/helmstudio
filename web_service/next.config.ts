import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  // no next/image anywhere (plain <img> of signed links), so the optimizer and sharp's ~18 MB of libraries stay out
  // of the standalone image
  images: { unoptimized: true },
  outputFileTracingExcludes: { "*": ["node_modules/sharp/**", "node_modules/@img/**"] },
  agentRules: false, // next dev would write AGENTS.md; the repo has CLAUDE.md
  allowedDevOrigins: ["127.0.0.1"], // Next 16 blocks dev assets for other hosts
};

export default nextConfig;
