// A golden is only comparable if the page has stopped changing. These pages
// stream a log, fetch thumbnails and lay out a grid, all of which finish at
// slightly different moments from run to run — and the goldens are exact, so a
// screenshot taken one frame early is a gate failure with no bug behind it.
//
// Rather than guess a timeout, wait until the rendered markup and every image
// have been the same across two polls, then give the compositor two frames.

/** shadows walks the light DOM and every open shadow root. */
function markup(root, out) {
  out.push(root.innerHTML ?? "");
  for (const node of root.querySelectorAll("*")) {
    if (node.shadowRoot) markup(node.shadowRoot, out);
  }
  return out;
}

function images(root, out) {
  for (const node of root.querySelectorAll("img")) out.push(node.complete && node.naturalWidth > 0);
  for (const node of root.querySelectorAll("*")) {
    if (node.shadowRoot) images(node.shadowRoot, out);
  }
  return out;
}

const frame = () => new Promise((r) => requestAnimationFrame(r));

export async function settle({ timeoutMs = 8000, pollMs = 50 } = {}) {
  const started = Date.now();
  let last = null;
  for (;;) {
    await new Promise((r) => setTimeout(r, pollMs));
    const imgs = images(document.body, []);
    const now = JSON.stringify([markup(document.body, []), imgs]);
    const loaded = imgs.every(Boolean);
    if (loaded && now === last) break;
    last = now;
    if (Date.now() - started > timeoutMs) {
      // Say so rather than shooting a page that never settled: a golden of a
      // half-drawn page would be pinned as if it were correct.
      document.documentElement.dataset.ready = "timeout";
      return;
    }
  }
  await frame();
  await frame();
  document.documentElement.dataset.ready = "1";
}
