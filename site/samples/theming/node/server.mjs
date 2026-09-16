// lantern studio's server in JavaScript: the same page and stylesheet, and the
// runtime SDK's proxy mounted at /helm/.
import http from "node:http";
import { readFile } from "node:fs/promises";
import { createProxy } from "@helmstudio/runtime/proxy";

const pages = {
  "/": ["index.html", "text/html; charset=utf-8"],
  "/studio.css": ["studio.css", "text/css; charset=utf-8"],
};
const port = Number(process.argv[process.argv.indexOf("--port") + 1]);

// HELM_API, HELM_SDK_BASE and the hue are set by helmstudio, or by helm dev.
const helmProxy = createProxy();

http.createServer(async (req, res) => {
  if (await helmProxy(req, res)) return;
  const path = new URL(req.url, "http://studio").pathname;
  if (path === "/healthz") return res.writeHead(200, { "Content-Type": "text/plain" }).end("ok");
  const page = pages[path];
  if (!page) return res.writeHead(404, { "Content-Type": "text/plain" }).end("not found");
  res.writeHead(200, { "Content-Type": page[1] }).end(await readFile(page[0]));
}).listen(port, "127.0.0.1", () => console.log(`lantern studio: http://127.0.0.1:${port}`));
