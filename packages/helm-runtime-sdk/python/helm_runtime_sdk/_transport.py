"""HTTP transport for the helmstudio runtime SDK. Standard library only."""

import datetime
import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, Iterator, Optional


def quote(value: str) -> str:
    """Escape one path segment."""
    return urllib.parse.quote(str(value), safe=":")


# The one typed error shape across languages (docs/design/04-packages.md §4).
KINDS = {
    400: "Invalid", 413: "Invalid", 416: "Invalid", 422: "Invalid",
    401: "Unauthenticated",
    403: "Forbidden", 421: "Forbidden",
    404: "NotFound", 410: "NotFound",
    409: "Conflict",
    429: "QuotaExceeded", 507: "QuotaExceeded",
    501: "Unsupported",
    503: "Unavailable",
}


class HelmError(Exception):
    """Every refusal: status, stable code, message and details."""

    def __init__(self, status: int, code: str, message: str, details: Optional[Dict[str, Any]] = None) -> None:
        super().__init__("%s (%d): %s" % (code, status, message))
        self.status = status
        self.code = code
        self.message = message
        self.details = details or {}

    @property
    def kind(self) -> str:
        if self.status == 0:
            return "Unavailable"
        return KINDS.get(self.status, "Internal")


class RawResponse:
    """An asset's bytes or a thumbnail."""

    def __init__(self, response: Any) -> None:
        self.status = response.status
        self.headers = dict(response.headers.items())
        self._response = response

    def read(self) -> bytes:
        try:
            return self._response.read()
        finally:
            self._response.close()


class Event:
    def __init__(self, event_id: str, name: str, data: str) -> None:
        self.id = event_id
        self.name = name
        self.data = data

    def json(self) -> Any:
        return json.loads(self.data) if self.data else None


def _events(response: Any) -> Iterator[Event]:
    event_id, name, data = "", "", []
    try:
        for raw in response:
            line = raw.decode("utf-8").rstrip("\r\n")
            if line == "":
                if name or data:
                    yield Event(event_id, name, "\n".join(data))
                event_id, name, data = "", "", []
            elif line.startswith(":"):
                continue
            elif line.startswith("id:"):
                event_id = line[3:].strip()
            elif line.startswith("event:"):
                name = line[6:].strip()
            elif line.startswith("data:"):
                data.append(line[5:].lstrip(" "))
    finally:
        response.close()


def _value(v: Any) -> str:
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, datetime.datetime):
        return v.isoformat()
    return str(v)


class Transport:
    def __init__(self, base: str, token: str, timeout: float = 60.0) -> None:
        self.base = base.rstrip("/")
        self.token = token
        self.timeout = timeout

    def request(self, method: str, path: str, query: Dict[str, Any], headers: Dict[str, Any], expect: str,
                json_body: Any = None, raw_body: Optional[bytes] = None, content_type: Optional[str] = None) -> Any:
        pairs = []
        for k, v in query.items():
            if v is None:
                continue
            if isinstance(v, (list, tuple)):
                pairs.extend((k, _value(x)) for x in v)
            else:
                pairs.append((k, _value(v)))
        url = self.base + path + ("?" + urllib.parse.urlencode(pairs) if pairs else "")
        body = None
        if json_body is not None:
            body = json.dumps(json_body).encode("utf-8")
        elif raw_body is not None:
            body = raw_body
        req = urllib.request.Request(url, data=body, method=method)
        req.add_header("Authorization", "Bearer " + self.token)
        if content_type:
            req.add_header("Content-Type", content_type)
        for k, v in headers.items():
            if v is not None:
                req.add_header(k, _value(v))
        try:
            response = urllib.request.urlopen(req, timeout=None if expect == "sse" else self.timeout)
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                doc = json.loads(raw)
                raise HelmError(e.code, doc.get("error", "unexpected_response"), doc.get("message", ""), doc.get("details")) from None
            except ValueError:
                raise HelmError(e.code, "unexpected_response", raw.decode("utf-8", "replace")) from None
        except urllib.error.URLError as e:
            raise HelmError(0, "unavailable", str(e.reason)) from None
        if expect == "sse":
            return _events(response)
        if expect == "raw":
            return RawResponse(response)
        with response:
            raw = response.read()
        if expect == "json":
            return json.loads(raw)
        return None
