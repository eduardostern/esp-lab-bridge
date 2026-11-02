package main

import (
	"fmt"
	"net/http"
)

func pageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	fmt.Fprint(w, pageHTML)
}

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>ESP Lab Bridge</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #080a0d; color: #c7d0db; font: 14px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace; }
  main { width: min(560px, calc(100vw - 32px)); border: 1px solid #1d232c; border-radius: 14px; background: #0e1116; padding: 28px; }
  h1 { margin: 0; font-size: 18px; color: #f3f6fa; letter-spacing: -0.02em; }
  p { margin: 12px 0 0; color: #77828f; }
  .status { margin-top: 24px; padding: 14px; border: 1px solid #1d232c; border-radius: 10px; background: #080a0d; }
  .dot { display: inline-block; width: 8px; height: 8px; margin-right: 8px; border-radius: 999px; background: #77828f; }
  .ok .dot { background: #b8f36a; box-shadow: 0 0 12px rgba(184,243,106,.55); }
  .bad .dot { background: #ff5c7a; }
  .url { display: none; margin-top: 16px; }
  .url a { display: block; padding: 12px 14px; border: 1px solid rgba(184,243,106,.35); border-radius: 10px; background: rgba(184,243,106,.08); color: #b8f36a; text-decoration: none; }
  .url a:hover { background: rgba(184,243,106,.14); }
  small { display: block; margin-top: 18px; color: #4b5563; font-size: 11px; }
</style>
</head>
<body>
<main>
  <h1>ESP Lab Bridge</h1>
  <p>Keep this running while you use Tasmota on the hosted ESP Lab.</p>
  <div id="status" class="status"><span class="dot"></span><span id="text">checking…</span></div>
  <div id="url" class="url"><a id="link" href="#">Open Tasmota web UI ↗</a></div>
  <small>Loopback only: 127.0.0.1:8788 · sessions stop after 10 minutes or when replaced.</small>
</main>
<script>
  const box = document.getElementById('status');
  const text = document.getElementById('text');
  const url = document.getElementById('url');
  const link = document.getElementById('link');

  async function tick() {
    try {
      const response = await fetch('/health', { cache: 'no-store' });
      const health = await response.json();
      box.className = health.engine ? 'status ok' : 'status bad';
      text.textContent = health.engine ? 'Ready — engine found' : 'Engine not found — put esp32sim next to the bridge';
      if (health.session && health.session.url) {
        url.style.display = 'block';
        link.href = health.session.url;
        link.textContent = 'Open Tasmota web UI — ' + health.session.url;
      } else {
        url.style.display = 'none';
        link.removeAttribute('href');
      }
    } catch (error) {
      box.className = 'status bad';
      text.textContent = 'Bridge is not responding';
      url.style.display = 'none';
    }
  }

  tick();
  setInterval(tick, 2000);
</script>
</body>
</html>
`
