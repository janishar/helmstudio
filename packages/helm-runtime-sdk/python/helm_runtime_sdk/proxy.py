"""The same-origin proxy a studio mounts at /helm/, so its page never holds a token.

Standard library only. It behaves exactly as the Go and Node runtime SDKs'
proxies do (docs/decisions.md M6 Q10; test/conformance holds all three to it):

- ``/helm/api/v1/<studio-api path>`` is forwarded to ``HELM_API`` with
  ``HELM_TOKEN`` added. Only the studio API and the theme stream; a launcher
  path is 404 and never reaches the daemon.
- ``/helm/sdk/v1/<file>`` (GET and HEAD) is forwarded to ``HELM_SDK_BASE``
  without a token.
- ``/helm/accent.css`` is the studio's hue for each theme.

The page's own Authorization, cookies, Origin and Referer are never forwarded,
and the daemon's Set-Cookie never comes back.

For a stdlib ``http.server`` studio::

    from helm_runtime_sdk.proxy import Proxy
    proxy = Proxy.from_env()

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if proxy.handle_http(self):
                return
            ...

For a WSGI studio, mount ``proxy.wsgi`` under ``/helm``.
"""

import json
import os
import re
import urllib.error
import urllib.request
from typing import Dict, Iterable, Iterator, List, Optional, Tuple

PREFIX = "/helm/"

# First path segments under /api/v1/ that are forwarded: every studio-api
# operation's, and the theme stream's.
STUDIO_SEGMENTS = frozenset(
    [
        "me", "events", "kv", "sessions", "records", "assets", "assets:adopt", "gallery",
        "handoff", "inbox", "jobs", "theme", "timeline", "timeline:append",
    ]
)

REQUEST_HEADERS = ["Accept", "Content-Type", "Range", "If-Range", "If-Match", "If-None-Match", "Last-Event-ID"]
RESPONSE_HEADERS = ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control", "Content-Disposition", "Allow"]
METHODS = frozenset(["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"])

_HEX = re.compile(r"^#[0-9a-fA-F]{6}$")


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):  # never follow: forward as is
        return None


_opener = urllib.request.build_opener(_NoRedirect)

Response = Tuple[int, List[Tuple[str, str]], Iterable[bytes]]


def _luminance(hex_colour: str) -> float:
    out = []
    for i in range(3):
        c = int(hex_colour[1 + 2 * i : 3 + 2 * i], 16) / 255
        out.append(c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4)
    return 0.2126 * out[0] + 0.7152 * out[1] + 0.0722 * out[2]


def _on(hex_colour: str) -> str:
    lum = _luminance(hex_colour)
    dark = (lum + 0.05) / (_luminance("#1a1400") + 0.05)
    white = 1.05 / (lum + 0.05)
    return "#1a1400" if dark >= white else "#ffffff"


def accent_css(dark: str, light: str) -> Optional[str]:
    """/helm/accent.css for a hue pair, or None unless both are #rrggbb."""
    if not dark or not light or not _HEX.match(dark) or not _HEX.match(light):
        return None
    dark, light = dark.lower(), light.lower()

    def block(h: str) -> str:
        return "--helm-studio-accent: " + h + "; --helm-on-studio-accent: " + _on(h) + ";"

    return (
        ":root { " + block(dark) + " }\n"
        + "@media (prefers-color-scheme: light) { :root:not([data-theme]) { " + block(light) + " } }\n"
        + ':root[data-theme="light"] { ' + block(light) + " }\n"
    )


def safe_path(path: str) -> bool:
    """Refuse dot segments, empty segments and encoded slashes, backslashes or dots."""
    lower = path.lower()
    if "%2f" in lower or "%5c" in lower or "%2e" in lower or "\\" in path:
        return False
    segs = path.lstrip("/").split("/")
    for i, s in enumerate(segs):
        if s in (".", "..") or (s == "" and i != len(segs) - 1):
            return False
    return True


def _error(status: int, code: str, message: str) -> Response:
    body = json.dumps({"error": code, "message": message}).encode() + b"\n"
    return status, [("Content-Type", "application/json"), ("X-Content-Type-Options", "nosniff")], [body]


def _not_found() -> Response:
    return _error(404, "not_found", "the helm proxy forwards only the studio API, the theme stream, the SDK files and accent.css")


class Proxy:
    def __init__(self, api: str = "", token: str = "", sdk_base: str = "", accent_dark: str = "", accent_light: str = "") -> None:
        self.api = api.rstrip("/")
        self.token = token
        self.sdk_base = sdk_base.rstrip("/")
        self.accent_dark = accent_dark
        self.accent_light = accent_light

    @classmethod
    def from_env(cls, env: Optional[Dict[str, str]] = None) -> "Proxy":
        e = os.environ if env is None else env
        return cls(e.get("HELM_API", ""), e.get("HELM_TOKEN", ""), e.get("HELM_SDK_BASE", ""),
                   e.get("HELM_ACCENT_DARK", ""), e.get("HELM_ACCENT_LIGHT", ""))

    def respond(self, method: str, raw_path: str, query: str, headers: Dict[str, str], body: Optional[bytes]) -> Response:
        """Answer one request. raw_path is the escaped path, starting /helm/."""
        if not raw_path.startswith(PREFIX):
            return _not_found()
        rest = raw_path[len(PREFIX) - 1 :]
        if not safe_path(rest):
            return _not_found()
        if rest == "/accent.css":
            css = accent_css(self.accent_dark, self.accent_light)
            if css is None or method not in ("GET", "HEAD"):
                return _not_found()
            payload = [] if method == "HEAD" else [css.encode()]
            return 200, [("Content-Type", "text/css; charset=utf-8"), ("Cache-Control", "no-cache"), ("X-Content-Type-Options", "nosniff")], payload
        if rest.startswith("/api/v1/"):
            sub = rest[len("/api/v1/") :]
            first = sub.split("/", 1)[0]
            if not self.api or first not in STUDIO_SEGMENTS:
                return _not_found()
            return self._forward(method, self.api + "/" + sub, query, headers, body, self.token)
        if rest.startswith("/sdk/v1/"):
            if not self.sdk_base or method not in ("GET", "HEAD"):
                return _not_found()
            return self._forward(method, self.sdk_base + "/" + rest[len("/sdk/v1/") :], query, headers, None, "")
        return _not_found()

    def _forward(self, method: str, target: str, query: str, headers: Dict[str, str], body: Optional[bytes], token: str) -> Response:
        if method not in METHODS:
            return _error(405, "method_not_allowed", method + " is not forwarded")
        if query:
            target += "?" + query
        lower = {k.lower(): v for k, v in headers.items()}
        out = {}
        for h in REQUEST_HEADERS:
            if h.lower() in lower:
                out[h] = lower[h.lower()]
        if token:
            out["Authorization"] = "Bearer " + token
        data = body if method not in ("GET", "HEAD") else None
        if method in ("POST", "PUT", "PATCH") and data is None:
            data = b""
        req = urllib.request.Request(target, data=data, method=method, headers=out)
        try:
            res = _opener.open(req)
        except urllib.error.HTTPError as e:
            res = e
        except (urllib.error.URLError, OSError):
            # Never the error text: it can carry the upstream URL.
            return _error(503, "unavailable", "helmstudio did not answer")
        status = res.status if hasattr(res, "status") else res.code
        resp_headers = [("X-Content-Type-Options", "nosniff")]
        for h in RESPONSE_HEADERS:
            for v in res.headers.get_all(h) or []:
                resp_headers.append((h, v))
        if method == "HEAD":
            res.close()
            return status, resp_headers, []
        return status, resp_headers, _chunks(res)

    def wsgi(self, environ, start_response):
        """A WSGI application for everything under /helm/."""
        raw = environ.get("RAW_URI") or environ.get("REQUEST_URI") or ""
        raw = raw.split("?", 1)[0] if raw else (environ.get("SCRIPT_NAME", "") + environ.get("PATH_INFO", ""))
        headers = {k[5:].replace("_", "-").title(): v for k, v in environ.items() if k.startswith("HTTP_")}
        if environ.get("CONTENT_TYPE"):
            headers["Content-Type"] = environ["CONTENT_TYPE"]
        length = int(environ.get("CONTENT_LENGTH") or 0)
        body = environ["wsgi.input"].read(length) if length else None
        status, hdrs, payload = self.respond(environ.get("REQUEST_METHOD", "GET"), raw, environ.get("QUERY_STRING", ""), headers, body)
        start_response("%d %s" % (status, _reason(status)), hdrs)
        return payload

    def handle_http(self, handler) -> bool:
        """Serve a request on a http.server.BaseHTTPRequestHandler if it is under /helm/.

        Returns False, having written nothing, for any other path.
        """
        path, _, query = handler.path.partition("?")
        if not path.startswith(PREFIX):
            return False
        length = int(handler.headers.get("Content-Length") or 0)
        body = handler.rfile.read(length) if length else None
        status, hdrs, payload = self.respond(handler.command, path, query, dict(handler.headers.items()), body)
        if isinstance(payload, list):
            # Fully known: say how long it is, so a kept-alive connection ends the response.
            data = b"".join(payload)
            payload = [data]
            if handler.command != "HEAD":
                hdrs = [(k, v) for k, v in hdrs if k != "Content-Length"] + [("Content-Length", str(len(data)))]
            elif not any(k == "Content-Length" for k, _ in hdrs):
                hdrs = hdrs + [("Content-Length", "0")]
        elif not any(k == "Content-Length" for k, _ in hdrs):
            # A stream of unknown length ends when the connection closes.
            handler.close_connection = True
            hdrs = hdrs + [("Connection", "close")]
        handler.send_response(status)
        for k, v in hdrs:
            handler.send_header(k, v)
        handler.end_headers()
        try:
            for chunk in payload:
                handler.wfile.write(chunk)
                handler.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            close = getattr(payload, "close", None)
            if close:
                close()
        return True


def _chunks(res) -> Iterator[bytes]:
    try:
        while True:
            chunk = res.read1(65536) if hasattr(res, "read1") else res.read(65536)
            if not chunk:
                return
            yield chunk
    finally:
        res.close()


def _reason(status: int) -> str:
    from http import HTTPStatus

    try:
        return HTTPStatus(status).phrase
    except ValueError:
        return ""
