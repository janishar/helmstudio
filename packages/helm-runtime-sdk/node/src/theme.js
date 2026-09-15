// The theme bridge: follows the launcher's theme in a studio's page
// (docs/design/03-design-system.md §5, 07-platform-services.md §6;
// docs/decisions.md M6 Q9).
//
//   import { themeBridge } from "/helm/sdk/v1/helm-runtime.js";
//   themeBridge();
//
// It listens to the tokenless theme stream — through the studio's same-origin
// proxy at /helm/ by default — and sets data-theme on the root element to
// "light" or "dark", or removes it for "system" so prefers-color-scheme
// applies. With no stream (a studio running standalone) data-theme is left as
// the page had it. No Node built-ins: this runs in a browser.

const THEMES = new Set(["system", "light", "dark"]);

// While a stream answers, a studio's own theme control is hidden (M6 Q19):
// the root carries data-helm-theme-follows="launcher".
const FOLLOWS = "data-helm-theme-follows";

export function applyTheme(theme, root = globalThis.document && globalThis.document.documentElement) {
  if (!root || !THEMES.has(theme)) return false;
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
  return true;
}

export function themeBridge({
  url = "/helm/api/v1/theme/events",
  root = globalThis.document && globalThis.document.documentElement,
  EventSource: ES = globalThis.EventSource,
  onTheme,
} = {}) {
  if (!ES || !root) return { close() {}, get following() { return false; } };
  let following = false;
  const source = new ES(url);
  source.addEventListener("theme", (e) => {
    let theme;
    try {
      theme = JSON.parse(e.data).theme;
    } catch {
      return;
    }
    if (!applyTheme(theme, root)) return;
    if (!following) {
      following = true;
      root.setAttribute(FOLLOWS, "launcher");
    }
    if (onTheme) onTheme(theme);
  });
  source.addEventListener("error", () => {
    // EventSource reconnects by itself after a dropped connection. A stream
    // that is not there at all (404, standalone) closes: stop claiming to
    // follow, so the studio's own control comes back.
    if (source.readyState === 2 && following) {
      following = false;
      root.removeAttribute(FOLLOWS);
    }
  });
  return {
    close() {
      source.close();
      if (following) root.removeAttribute(FOLLOWS);
      following = false;
    },
    get following() {
      return following;
    },
  };
}
