import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  // signed /files links are opaque and short-lived: plain <img>, no next/image optimisation
  { rules: { "@next/next/no-img-element": "off" } },
  globalIgnores([
    "node_modules/**",
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    "gen/**",
  ]),
]);
