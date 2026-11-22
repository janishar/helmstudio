// The web's edits for check.py's three-way replay (README, Timeline rules; plan 24 28.9): every case of
// fixtures/timelines/edits.json through web_service/lib/timeline.ts `apply` (node strips its types), written as JSON
// to TIMELINE_REPLAY_OUT.  node --no-warnings replay_edits.mjs <lib/timeline.ts> <edits.json>
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [lib, fixture] = process.argv.slice(2);
const { apply } = await import(pathToFileURL(lib).href);
const fx = JSON.parse(readFileSync(fixture, "utf8"));
const out = fx.cases.map((c) => apply(c.document, c.op, { ...(fx.takes ?? {}), ...(c.takes ?? {}) }));
writeFileSync(process.env.TIMELINE_REPLAY_OUT, JSON.stringify(out));
