// Smoke test of the Node client against a running daemon.
//
// Run by test/conformance (TestPythonAndNodeClientsSmoke) with HELM_API,
// HELM_TOKEN and HELM_STAGE_DIR set for a studio holding kv, records, assets
// and gallery. Exits non-zero, naming the failed check, on any mismatch.

import { writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { fromEnv, HelmError } from "../src/index.js";

function check(cond, what) {
  if (!cond) {
    console.log("FAIL: " + what);
    process.exit(1);
  }
}

async function refused(promise) {
  try {
    await promise;
  } catch (e) {
    return e;
  }
  return null;
}

const helm = fromEnv();
const me = await helm.me.get();
check(me.studio_id === process.env.SMOKE_STUDIO, "me.studio_id");

const doc = await helm.kv.put("ui", "node", { n: 1 });
let e = await refused(helm.kv.put("ui", "node", { n: 2 }, { ifMatch: "stale" }));
check(e instanceof HelmError && e.kind === "Conflict" && e.code === "etag_mismatch", "etag conflict kind");
const patched = await helm.kv.patch("ui", "node", { m: 2 }, { ifMatch: doc.etag });
check(patched.doc.n === 1 && patched.doc.m === 2, "merge patch");

const rec = await helm.records.insert("takes", { seed: 7 });
const page = await helm.records.query("takes", { where: ["seed:eq:7"] });
check(page.items.length === 1 && page.items[0].id === rec.id, "records filter");

const staged = join(process.env.HELM_STAGE_DIR, "node-take.mp4");
writeFileSync(staged, "node take");
const asset = await helm.assets.adopt({ path: staged, kind: "video" });
check(!existsSync(staged), "adopt unlinked the stage entry");
const part = await helm.assets.read(asset.id, { range: "bytes=0-3" });
check(part.status === 206 && (await part.text()) === "node", "range read");

const item = await helm.gallery.add({ kind: "video", asset_id: asset.id, params: { seed: 7 }, tags: ["node"] });
const found = await helm.gallery.query({ tag: ["node"] });
check(found.items.length === 1 && found.items[0].id === item.id, "gallery tag query");

e = await refused(helm.jobs.list());
check(e instanceof HelmError && e.kind === "Forbidden" && e.details.capability === "jobs", "capability_required details");

console.log("ok");
