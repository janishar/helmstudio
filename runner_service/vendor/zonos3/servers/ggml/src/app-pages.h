// Built-in pages for zonos3-app, shown via saucer's webview->set_html() before the
// embedded server is up (setup / loading / error). Self-contained HTML: no network,
// no external assets. The pages call C++ through the functions app.cpp exposes on
// the smartview (`saucer.exposed.*`). ZONOS3_APP_ERROR_PAGE contains a %DETAILS%
// placeholder replaced (HTML-escaped) by app.cpp.
#pragma once

static const char * ZONOS3_APP_STYLE = R"html(
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    background: #101216; color: #e6e8ee;
    font: 15px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  }
  .card { width: min(560px, 92vw); background: #171a21; border: 1px solid #262b36;
          border-radius: 12px; padding: 28px 32px; }
  h1 { font-size: 20px; margin: 0 0 4px; }
  .sub { color: #8b93a7; font-size: 13px; margin: 0 0 22px; }
  label { display: block; font-size: 13px; color: #aab1c2; margin: 14px 0 4px; }
  .row { display: flex; gap: 8px; }
  input[type=text] { flex: 1; background: #0d0f13; color: #e6e8ee; border: 1px solid #2a3040;
                     border-radius: 7px; padding: 8px 10px; font-size: 13px; min-width: 0; }
  input[type=text]:focus { outline: none; border-color: #5b7fd4; }
  select { width: 100%; background: #0d0f13; color: #e6e8ee; border: 1px solid #2a3040;
           border-radius: 7px; padding: 8px 10px; font-size: 13px; }
  select:focus { outline: none; border-color: #5b7fd4; }
  a { color: #7fa0ff; text-decoration: none; }
  a:hover { text-decoration: underline; }
  button { background: #222836; color: #e6e8ee; border: 1px solid #323a4d; border-radius: 7px;
           padding: 8px 14px; font-size: 13px; cursor: pointer; white-space: nowrap; }
  button:hover { background: #2a3242; }
  button.primary { background: #3b5bd6; border-color: #3b5bd6; font-weight: 600;
                   width: 100%; margin-top: 22px; padding: 10px; font-size: 14px; }
  button.primary:hover { background: #4a69e2; }
  button:disabled { opacity: .5; cursor: default; }
  .check { display: flex; align-items: center; gap: 8px; margin-top: 16px; font-size: 14px; }
  .err { color: #ff8d8d; font-size: 13px; margin-top: 14px; white-space: pre-wrap; display: none; }
  .spinner { width: 44px; height: 44px; margin: 0 auto 18px; border-radius: 50%;
             border: 3px solid #2a3040; border-top-color: #5b7fd4; animation: spin 0.9s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .center { text-align: center; }
  pre.details { background: #0d0f13; border: 1px solid #2a3040; border-radius: 7px; padding: 12px;
                font-size: 12px; white-space: pre-wrap; word-break: break-word; color: #c3c9d8;
                max-height: 200px; overflow: auto; text-align: left; }
</style>
)html";

// ---------------------------------------------------------------- setup (first run)

static const char * ZONOS3_APP_SETUP_PAGE = R"html(
<div class="card">
  <div id="main">
    <h1>Zonos3</h1>
    <p class="sub">Select local GGUF files to get started.</p>

    <div id="manual-view">
      <label>Backbone model (.gguf)</label>
      <div class="row"><input type="text" id="model_in" placeholder="backbone-ptq.gguf">
        <button onclick="browse('model')">Browse&hellip;</button></div>
      <label>ZAC decoder (.gguf)</label>
      <div class="row"><input type="text" id="zac_in" placeholder="zac-keep2.gguf">
        <button onclick="browse('zac')">Browse&hellip;</button></div>
      <label>Speaker encoder (.gguf, optional; speaker-embedding cloning)</label>
      <div class="row"><input type="text" id="spk_encoder_in" placeholder="spk-encoder.gguf">
        <button onclick="browse('spk_encoder')">Browse&hellip;</button></div>
      <label>ZAC encoder (.gguf, optional &mdash; raw-reference cloning needs both ZAC encoder and SSL teacher)</label>
      <div class="row"><input type="text" id="zac_encoder_in" placeholder="zac-encoder.gguf">
        <button onclick="browse('zac_encoder')">Browse&hellip;</button></div>
      <label>SSL teacher (.gguf, optional)</label>
      <div class="row"><input type="text" id="ssl_teacher_in" placeholder="ssl-teacher.gguf">
        <button onclick="browse('ssl_teacher')">Browse&hellip;</button></div>
    </div>

    <div class="check"><input type="checkbox" id="gpu" checked>
      <label for="gpu" style="margin:0">Use GPU</label></div>

    <div class="err" id="err"></div>
    <button class="primary" id="launchbtn" onclick="launchManual()">Save &amp; Launch</button>
  </div>

</div>
<script>
  const $ = id => document.getElementById(id);
  function fail(msg) { $('err').textContent = msg; $('err').style.display = 'block'; }
  function clearErr() { $('err').style.display = 'none'; }
  async function browse(kind) {
    try { const p = await saucer.exposed.pick_file(kind); if (p) $(kind + '_in').value = p; }
    catch (e) { fail(String(e)); }
  }

  async function doLaunch(model, zac, encoder, teacher, spk, gpu) {
    const err = await saucer.exposed.launch(model, zac, encoder, teacher, spk, gpu);
    if (err) throw new Error(err);   // C++ swaps to the splash page on success
  }

  async function launchManual() {
    clearErr();
    const model = $('model_in').value.trim(), zac = $('zac_in').value.trim(), encoder = $('zac_encoder_in').value.trim(), teacher = $('ssl_teacher_in').value.trim(), spk = $('spk_encoder_in').value.trim();
    if (!model || !zac) { fail('Backbone model and ZAC decoder are required.'); return; }
    $('launchbtn').disabled = true;
    try { await doLaunch(model, zac, encoder, teacher, spk, $('gpu').checked); }
    catch (e) { fail(String(e.message || e)); $('launchbtn').disabled = false; }
  }


  (async () => {
    try {
      const cfg = JSON.parse(await saucer.exposed.get_config());
      if (cfg.model) $('model_in').value = cfg.model;
      if (cfg.zac)   $('zac_in').value   = cfg.zac;
      if (cfg.zac_encoder) $('zac_encoder_in').value = cfg.zac_encoder;
      if (cfg.ssl_teacher) $('ssl_teacher_in').value = cfg.ssl_teacher;
      if (cfg.spk_encoder) $('spk_encoder_in').value = cfg.spk_encoder;
      $('gpu').checked = cfg.gpu !== false;
    } catch (e) { /* first run: no config yet */ }
  })();
</script>
)html";

// ---------------------------------------------------------------- loading splash

static const char * ZONOS3_APP_SPLASH_PAGE = R"html(
<div class="card center">
  <div class="spinner"></div>
  <h1>Loading models&hellip;</h1>
  <p class="sub" id="status" style="margin-bottom:0">This can take a little while on first launch.</p>
</div>
)html";

// ---------------------------------------------------------------- startup error

static const char * ZONOS3_APP_ERROR_PAGE = R"html(
<div class="card center">
  <h1>Failed to start</h1>
  <p class="sub">The embedded zonos3 server did not come up.</p>
  <pre class="details">%DETAILS%</pre>
  <button class="primary" onclick="saucer.exposed.show_setup()">Back to setup</button>
</div>
)html";
