// The web's Magic edits for check.py's replay (README, Magic: edits by ops): every case of fixtures/magic/edits.json
// through web_service/lib/magic.ts `apply`, op after op, all or nothing (node strips its types), written as JSON to
// MAGIC_REPLAY_OUT: the document after the last op, or {error, op_index} of the first that cannot apply.
//   node --no-warnings replay_magic.mjs <lib/magic.ts> <edits.json>
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [lib, fixture] = process.argv.slice(2);
const { apply } = await import(pathToFileURL(lib).href);
const fx = JSON.parse(readFileSync(fixture, "utf8"));
const out = fx.cases.map((c) => {
  let doc = c.document ? fx.documents[c.document] : c.doc;
  for (const [i, op] of c.ops.entries()) {
    const r = apply(doc, op);
    if ("error" in r) return { error: r.error, op_index: i };
    doc = r.doc;
  }
  return doc;
});
writeFileSync(process.env.MAGIC_REPLAY_OUT, JSON.stringify(out));
