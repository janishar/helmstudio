// The plain shelf. No framework, no styling: list, launch, stop, logs.
"use strict";

const api = "/api/v1";
const maxLogLines = 2000;
let logSource = null;
let logLines = [];

async function call(method, path) {
  const res = await fetch(api + path, { method });
  const body = await res.json().catch(() => ({}));
  return { ok: res.ok, status: res.status, body };
}

function el(tag, text) {
  const e = document.createElement(tag);
  if (text !== undefined) e.textContent = text;
  return e;
}

function elapsed(since) {
  const s = Math.max(0, Math.floor((Date.now() - new Date(since).getTime()) / 1000));
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

function processLine(studio, p) {
  let text = p.spec_name + " (" + p.role + "): " + p.state;
  if (p.state === "starting" && p.started_at && p.health_timeout_s) {
    text += " " + elapsed(p.started_at) + " of " + Math.floor(p.health_timeout_s / 60) + ":" + String(p.health_timeout_s % 60).padStart(2, "0");
  } else if (p.state === "running" && p.started_at) {
    text += " " + elapsed(p.started_at);
  }
  if ((p.state === "starting" || p.state === "running") && p.health_state && p.health_state !== "unknown") text += ", health " + p.health_state;
  if (p.exit_reason) text += ", " + p.exit_reason;
  if (p.exit_code !== undefined && p.exit_code !== null) text += " exit " + p.exit_code;
  if (p.pid) text += ", pid " + p.pid;
  if (p.port) text += ", port " + p.port;

  const li = el("li", text + " ");
  if (p.state === "running" && p.port && p.ui) {
    const a = el("a", "Open");
    a.href = "http://127.0.0.1:" + p.port + p.ui;
    a.target = "_blank";
    a.rel = "noopener";
    li.append(a, " ");
  }
  const logs = el("button", "Logs");
  logs.onclick = () => showLogs(studio, p.spec_name);
  li.append(logs);
  return li;
}

async function launch(studio, preempt) {
  const r = await call("POST", "/studios/" + encodeURIComponent(studio.id) + ":launch" + (preempt ? "?preempt=true" : ""));
  if (r.ok) return refresh();
  if (r.body.error === "heavy_conflict" && !preempt) {
    if (confirm(r.body.message)) return launch(studio, true);
    return;
  }
  alert(r.body.message || "Launch failed (" + r.status + ")");
  refresh();
}

async function stop(studio) {
  const r = await call("POST", "/studios/" + encodeURIComponent(studio.id) + ":stop");
  if (!r.ok) alert(r.body.message || "Stop failed (" + r.status + ")");
  refresh();
}

function render(studios) {
  const tbody = document.getElementById("studios");
  tbody.replaceChildren();
  for (const s of studios) {
    const tr = el("tr");
    const name = el("td");
    name.append(el("strong", s.name), el("br"), el("small", s.id + (s.heavy ? " · heavy" + (s.peak_ram_gb ? " · up to " + s.peak_ram_gb + " GB" : "") : "")));
    if (!s.manifest_loaded) name.append(el("br"), el("small", "Still running from the last daemon; its manifest is not loaded, so it can only be stopped."));
    else if (!s.root_present) name.append(el("br"), el("small", "No checkout at " + s.root));

    const state = el("td", s.group.state || "not launched");
    const procs = el("td");
    const ul = el("ul");
    for (const p of s.group.processes || []) ul.append(processLine(s, p));
    procs.append(ul);
    if (s.group.failure) {
      const f = s.group.failure;
      const details = el("details");
      details.append(el("summary", f.process + " failed: " + f.exit_reason + (f.exit_code != null ? " exit " + f.exit_code : "")));
      details.append(el("pre", (f.last_lines || []).join("\n")));
      procs.append(details);
    }

    const actions = el("td");
    const live = ["starting", "running", "stopping"].includes(s.group.state);
    const launchBtn = el("button", s.group.state === "failed" ? "Restart" : "Launch");
    launchBtn.disabled = live || !s.manifest_loaded;
    launchBtn.onclick = () => launch(s, false);
    const stopBtn = el("button", "Stop");
    stopBtn.disabled = !live || s.group.state === "stopping";
    stopBtn.onclick = () => stop(s);
    actions.append(launchBtn, " ", stopBtn);

    tr.append(name, state, procs, actions);
    tbody.append(tr);
  }
}

async function refresh() {
  const r = await call("GET", "/studios");
  const status = document.getElementById("status");
  if (!r.ok) {
    status.textContent = "Could not reach the daemon: " + (r.body.message || r.status);
    return;
  }
  status.textContent = r.body.length + " studios";
  render(r.body);
}

function showLogs(studio, process) {
  if (logSource) logSource.close();
  logLines = [];
  const pre = document.getElementById("log");
  const title = document.getElementById("log-title");
  const note = document.getElementById("log-note");
  title.textContent = "Log: " + studio.name + " / " + process;
  note.textContent = "Streaming. The last " + maxLogLines + " lines are shown.";
  for (const e of [pre, title, note]) e.hidden = false;
  pre.textContent = "";

  const append = (text) => {
    logLines.push(text);
    if (logLines.length > maxLogLines) logLines.splice(0, logLines.length - maxLogLines);
    const follow = window.innerHeight + window.scrollY >= document.body.scrollHeight - 20;
    pre.textContent = logLines.join("\n");
    if (follow) window.scrollTo(0, document.body.scrollHeight);
  };
  const url = api + "/studios/" + encodeURIComponent(studio.id) + "/processes/" + encodeURIComponent(process) + "/logs";
  logSource = new EventSource(url);
  logSource.addEventListener("line", (e) => append(JSON.parse(e.data).text || ""));
  logSource.addEventListener("gap", (e) => {
    const g = JSON.parse(e.data);
    append("[" + g.gap + " " + g.gap_unit + " not shown]");
  });
  logSource.addEventListener("end", (e) => {
    note.textContent = JSON.parse(e.data).reason;
    logSource.close();
  });
}

refresh();
setInterval(refresh, 2000);
