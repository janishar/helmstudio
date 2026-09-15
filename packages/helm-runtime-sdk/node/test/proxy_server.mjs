// A studio server that only mounts the helm proxy, for test/conformance.
//
// Reads HELM_API, HELM_TOKEN, HELM_SDK_BASE and HELM_ACCENT_* like a studio
// would, listens on a free loopback port, and prints "listening <port>".

import http from "node:http";
import { createProxy } from "../src/proxy.js";

const helmProxy = createProxy();
const server = http.createServer(async (req, res) => {
  if (await helmProxy(req, res)) return;
  res.writeHead(418, { "Content-Length": "0" });
  res.end();
});
server.listen(0, "127.0.0.1", () => {
  console.log(`listening ${server.address().port}`);
});
