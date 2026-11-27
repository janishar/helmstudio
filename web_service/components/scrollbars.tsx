"use client";
// Scrollbars show only while their area scrolls (globals.css: the thumb is clear until data-scrolling), as the
// Mac's own overlay bars do: one capture listener for every scroller, the mark dropped 0.8 s after the last scroll.
import { useEffect } from "react";

export function Scrollbars() {
  useEffect(() => {
    const timers = new WeakMap<Element, number>();
    const on = (e: Event) => {
      const el = e.target instanceof Element ? e.target : document.documentElement;
      el.setAttribute("data-scrolling", "");
      clearTimeout(timers.get(el));
      timers.set(
        el,
        window.setTimeout(() => el.removeAttribute("data-scrolling"), 800),
      );
    };
    document.addEventListener("scroll", on, { capture: true, passive: true });
    return () => document.removeEventListener("scroll", on, { capture: true });
  }, []);
  return null;
}
