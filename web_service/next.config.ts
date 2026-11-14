import type { NextConfig } from "next";

// Caching: every page and /api answer is per-user (read with the user's token), so cacheComponents, "use cache" and
// fetch caching stay off; pages are dynamic because the (app) layout reads cookies().
const nextConfig: NextConfig = {
  output: "standalone",
  // no next/image anywhere (plain <img> of signed links), so the optimizer and sharp's ~18 MB of libraries stay out
  // of the standalone image
  images: { unoptimized: true },
  outputFileTracingExcludes: { "*": ["node_modules/sharp/**", "node_modules/@img/**"] },
  agentRules: false, // next dev would write AGENTS.md; the repo has CLAUDE.md
  allowedDevOrigins: ["127.0.0.1"], // Next 16 blocks dev assets for other hosts
  poweredByHeader: false,
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          // no framing (clickjacking), plugins, <base> or off-site form posts. ponytail: no script-src/style-src yet
          // (inline style={} and next-themes' inline script); a nonce-based one via proxy.ts is the next step
          {
            key: "Content-Security-Policy",
            value: "frame-ancestors 'none'; object-src 'none'; base-uri 'self'; form-action 'self'",
          },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
        ],
      },
    ];
  },
};

export default nextConfig;
