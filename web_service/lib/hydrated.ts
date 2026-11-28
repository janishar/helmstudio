import { useState, useSyncExternalStore } from "react";

export const noSubscribe = () => () => {};

/** False in the server render and while hydrating, true after: the viewer's locale and time zone aren't the server's,
 * so a local time is drawn only once this is true. */
export const useHydrated = () =>
  useSyncExternalStore(
    noSubscribe,
    () => true,
    () => false,
  );

/** This browser's store, never throwing (a private window, a full or blocked store, the server render): a miss is
 * null, a failed write is dropped. `set(key, null)` removes the key. */
export const stored = {
  get(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key: string, value: string | null) {
    try {
      if (value === null) localStorage.removeItem(key);
      else localStorage.setItem(key, value);
    } catch {}
  },
  /** The parsed JSON at `key`, or null (missing, unreadable, not JSON). */
  json<T>(key: string): T | null {
    try {
      return JSON.parse(stored.get(key) ?? "null") as T | null;
    } catch {
      return null;
    }
  },
};

/** A flag on by default whose "off" is remembered in this browser ("0"): the side panels and the settings drawer.
 * Returns the flag, its setter (remembered) and a setter for this visit only. */
export function useStoredFlag(key: string) {
  const saved = useSyncExternalStore(
    noSubscribe,
    () => stored.get(key) !== "0",
    () => true,
  );
  const [set, setOnly] = useState<boolean | null>(null);
  const save = (on: boolean) => {
    setOnly(on);
    stored.set(key, on ? "1" : "0");
  };
  return [set ?? saved, save, setOnly as (on: boolean) => void] as const;
}
