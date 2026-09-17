// Drawing a screen again without throwing the page away (docs/decisions.md,
// 2026-09-17 · The launcher redesign).
//
// A screen is still a function of the store that returns fresh nodes, which
// is what lets a fixture draw any screen from canned data. What changed is
// what happens to those nodes. The launcher used to swap the whole page for
// them, and every swap cost the caret, the focused button, an open menu and
// the scroll position of every pane — so it redrew as seldom as it could, and
// a field the change signature forgot was a field that never updated.
//
// morph walks the new tree against the page and changes only what differs.
// An element that is still there is the same element afterwards, so focus, an
// open menu and a half-scrolled list survive a poll.
//
// Three rules make that true:
//
//   - A listener lives on its node (`listen`) rather than being bound to it,
//     so an element that stays takes the new screen's handlers along with its
//     attributes. It follows that a handler must not close over an element
//     from its own render — that element never reaches the page. A handler
//     reads `event.currentTarget`, its screen's state, or an id.
//   - A node a screen keeps across redraws (`ctx.keep`) is marked, and is put
//     in place as itself rather than morphed into whatever held its slot.
//   - Siblings are matched by `data-key` where there is one, and by position
//     and tag otherwise. A list whose rows can move gives each row a key.

const HANDLERS = Symbol("helm.handlers");
const KEPT = Symbol("helm.kept");

/** listen attaches a handler that a later redraw can replace. */
export function listen(node, type, fn) {
  let handlers = node[HANDLERS];
  if (!handlers) {
    handlers = Object.create(null);
    node[HANDLERS] = handlers;
  }
  if (!(type in handlers)) node.addEventListener(type, dispatch);
  handlers[type] = fn;
}

function dispatch(event) {
  const fn = this[HANDLERS] && this[HANDLERS][event.type];
  return fn ? fn.call(this, event) : undefined;
}

/** kept marks a node that is the same instance on every redraw. */
export function kept(node) {
  node[KEPT] = true;
  return node;
}

function keyOf(node) {
  return node.nodeType === Node.ELEMENT_NODE && node.hasAttribute("data-key") ? node.getAttribute("data-key") : null;
}

function alike(a, b) {
  return a.nodeType === b.nodeType && a.nodeName === b.nodeName && !a[KEPT] && !b[KEPT];
}

/**
 * morph makes `live`, which is on the page, into `next`, which is not, and
 * returns whichever node is on the page afterwards.
 */
export function morph(live, next) {
  if (live === next) return live;
  if (!alike(live, next)) {
    live.replaceWith(next);
    return next;
  }
  if (live.nodeType !== Node.ELEMENT_NODE) {
    if (live.nodeValue !== next.nodeValue) live.nodeValue = next.nodeValue;
    return live;
  }
  attributes(live, next);
  handlers(live, next);
  morphChildren(live, [...next.childNodes]);
  formState(live, next);
  return live;
}

/** morphChildren makes `parent`'s children the `wanted` list, in order. */
export function morphChildren(parent, wanted) {
  const keyed = new Map();
  for (const child of parent.childNodes) {
    const key = keyOf(child);
    if (key !== null) keyed.set(key, child);
  }
  let cursor = parent.firstChild;
  for (const want of wanted) {
    let node = want;
    if (!want[KEPT]) {
      const key = keyOf(want);
      if (key !== null) {
        const found = keyed.get(key);
        if (found && alike(found, want)) {
          keyed.delete(key);
          node = morph(found, want);
        }
      } else if (cursor && keyOf(cursor) === null && alike(cursor, want)) {
        node = morph(cursor, want);
      }
    }
    if (node === cursor) cursor = cursor.nextSibling;
    else parent.insertBefore(node, cursor);
  }
  while (cursor) {
    const after = cursor.nextSibling;
    parent.removeChild(cursor);
    cursor = after;
  }
}

function attributes(live, next) {
  for (const { name } of [...live.attributes]) {
    if (!next.hasAttribute(name)) live.removeAttribute(name);
  }
  for (const { name, value } of [...next.attributes]) {
    if (live.getAttribute(name) !== value) live.setAttribute(name, value);
  }
}

function handlers(live, next) {
  const want = next[HANDLERS] || {};
  const have = live[HANDLERS];
  if (have) {
    for (const type of Object.keys(have)) {
      if (!(type in want)) have[type] = null;
    }
  }
  for (const type of Object.keys(want)) listen(live, type, want[type]);
}

/**
 * formState carries what a control shows, which is a property rather than an
 * attribute once someone has touched it. Never under the caret: a redraw that
 * rewrote the field being typed into would be the bug this file exists to end.
 */
function formState(live, next) {
  if (live === document.activeElement) return;
  switch (live.nodeName) {
    case "INPUT":
      if (live.type === "checkbox" || live.type === "radio") {
        if (live.checked !== next.checked) live.checked = next.checked;
      } else if (live.type !== "file" && live.value !== next.value) {
        live.value = next.value;
      }
      break;
    case "TEXTAREA":
      if (live.value !== next.value) live.value = next.value;
      break;
    case "SELECT":
      if (live.selectedIndex !== next.selectedIndex) live.selectedIndex = next.selectedIndex;
      break;
    default:
      break;
  }
}
