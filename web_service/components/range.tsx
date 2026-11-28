import type { CSSProperties, InputHTMLAttributes } from "react";

// The one slider (owner, 2026-11-28): a native range input whose --z (fill percent) always follows its own value,
// so the filled track in globals.css matches the handle, also when the value changes in code.
export function Range({ style, ...p }: Omit<InputHTMLAttributes<HTMLInputElement>, "type">) {
  const min = Number(p.min ?? 0);
  const max = Number(p.max ?? 100);
  const v = Number(p.value ?? p.defaultValue ?? min);
  const z = max > min ? Math.min(100, Math.max(0, ((v - min) / (max - min)) * 100)) : 0;
  return <input {...p} type="range" style={{ ...style, "--z": `${z}%` } as CSSProperties} />;
}
