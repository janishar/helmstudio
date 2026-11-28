// Create's optional add-on gates (API 1.36, 1.40, 1.41; contracts/README.md, Optional add-ons): what the chosen
// variants and Run on leave greyed, the bypasses that keeps, and the param values a missing add-on blocks.
import { useMemo } from "react";
import type { S } from "@/lib/api";
import {
  addonBlocks,
  addonNeeds,
  chosenVariants,
  machineReason,
  needed,
  valueBlock,
  valueNeeds,
  valueNeedsAt,
  type Machine,
} from "@/lib/run-on";
import { refCount } from "@/lib/refs";
import { toRaw, type Field, type Raw } from "@/lib/workflow";

export function useAddonGates(
  workflow: S<"Workflow"> | undefined,
  modelList: S<"ModelList"> | null,
  all: Field[],
  held: Record<string, Raw>,
  pins: Record<string, string>,
  machineList: Machine[],
  runOn: Machine | undefined,
) {
  // optional add-ons (API 1.36): a node whose add-on is not where the run may go has its controls greyed, and stays
  // bypassed so the run check never refuses it (contracts/README.md, Optional add-ons)
  // (API 1.40) an add-on scoped to other variants than the chosen one is not needed; one a node the workflow always
  // runs needs greys its variants' rows instead (Dev without the distilled LoRA)
  const allNeeds = useMemo(() => addonNeeds(workflow, modelList), [workflow, modelList]);
  const chosenModels = chosenVariants(all, { ...held, ...pins }, modelList);
  // (API 1.40) and one for the other task (`refs`): an edit needs Qwen's PE-I2I, text to image its PE-T2I
  const withRefs = refCount(all, held) > 0;
  const needs = allNeeds.filter((n) => needed(n, chosenModels, withRefs));
  const blocks = addonBlocks(needs, runOn);
  const forced = Object.fromEntries(Object.keys(blocks).map((n) => [`${n}.bypass`, true]));
  const raws =
    Object.keys(pins).length || Object.keys(forced).length ? { ...held, ...pins, ...forced } : held;
  // (API 1.41) an add-on one value of a param asks for (MiniMax H3's lora turbo: Turbo LoRA, with a reference Turbo
  // Ref LoRA): that option and every preset that picks it are greyed where the run may go, and a form that holds it
  // anyway keeps it, greyed, with Generate blocked (it never drops to another value by itself; H3Options 11-13)
  const allValueNeeds = useMemo(() => valueNeeds(workflow, modelList), [workflow, modelList]);
  const valueOn = (node: string) =>
    (raws[`${node}.bypass`] ?? workflow?.nodes[node]?.bypass) !== true;
  const wiredIn = (node: string) => (name: string) => {
    const v = raws[`${node}.${name}`] ?? all.find((f) => f.key === `${node}.${name}`)?.initial;
    return (Array.isArray(v) && v.length > 0) || !!workflow?.nodes[node]?.in?.[name];
  };
  const canRun = machineList.filter(
    (mc) => machineReason(mc, workflow?.env, chosenModels) === null,
  );
  const valueBlocks: Record<string, Record<string, string>> = {};
  const heldNeeds: typeof allValueNeeds = [];
  let heldBlock: string | null = null;
  for (const f of all.filter((x) =>
    allValueNeeds.some((n) => n.field === x.key && valueOn(n.node)),
  )) {
    const cur = String(raws[f.key] ?? toRaw(f.initial, f.param));
    for (const o of f.param.enum ?? []) {
      const why = valueBlock(allValueNeeds, f.key, o, wiredIn(f.node), canRun, runOn, chosenModels);
      if (why) (valueBlocks[f.key] ??= {})[o] = why;
    }
    heldNeeds.push(...valueNeedsAt(allValueNeeds, f.key, cur, wiredIn(f.node)));
    heldBlock ??= valueBlocks[f.key]?.[cur] ?? null;
  }
  return { allNeeds, needs, blocks, raws, valueBlocks, heldNeeds, heldBlock };
}
