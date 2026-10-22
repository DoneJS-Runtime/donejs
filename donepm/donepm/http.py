"""A tiny HTTP client with connection reuse.

npm's own client opens a fresh connection for nearly every metadata request;
reusing sockets is one of the cheap wins that makes DonePM fast.  Only the
standard library is used.
"""

from __future__ import annotations

import http.client
import json
import ssl
import threading
from typing import Any
from urllib.parse import urlsplit

from .errors import RegistryError

_JSON = {"Content-Type": "application/json"}


class Response:
    __slots__ = ("status", "headers", "body")

    def __init__(self, status: int, headers: Any, body: bytes):
        self.status = status
        self.headers = headers
        self.body = body

    @property
    def ok(self) -> bool:
        return 200 <= self.status < 300

    @property
    def text(self) -> str:
        return self.body.decode("utf-8", "replace")

    def json(self) -> Any:
        return json.loads(self.body)


class HttpClient:
    """Thread-safe pool of keep-alive connections."""

    def __init__(self, timeout: float = 30.0) -> None:
        self.timeout = timeout
        self._tls = threading.local()
        self._lock = threading.Lock()

    # -- connection pool --------------------------------------------------

    def _connection(self, scheme: str, host: str, port: int) -> http.client.HTTPConnection:
        pool = getattr(self._tls, "pool", None)
        if pool is None:
            pool = self._tls.pool = {}
        key = (scheme, host, port)
        conn = pool.get(key)
        if conn is not None:
            return conn
        if scheme == "https":
            ctx = ssl.create_default_context()
            conn = http.client.HTTPSConnection(host, port, timeout=self.timeout, context=ctx)
        else:
            conn = http.client.HTTPConnection(host, port, timeout=self.timeout)
        pool[key] = conn
        return conn

    def _drop(self, scheme: str, host: str, port: int) -> None:
        pool = getattr(self._tls, "pool", None)
        if pool:
            pool.pop((scheme, host, port), None)

    # -- requests ---------------------------------------------------------

    def request(
        self,
        method: str,
        url: str,
        *,
        headers: dict[str, str] | None = None,
        body: bytes | None = None,
        retries: int = 2,
    ) -> Response:
        parts = urlsplit(url)
        if parts.scheme not in {"http", "https"}:
            raise RegistryError(f"unsupported URL scheme in {url!r}")
        host = parts.hostname
        if host is None:
            raise RegistryError(f"missing host in {url!r}")
        port = parts.port or (443 if parts.scheme == "https" else 80)
        target = parts.path or "/"
        if parts.query:
            target += "?" + parts.query

        hdrs = {"Accept": "application/json", "User-Agent": "donepm/0.1"}
        if headers:
            hdrs.update(headers)

        last: Exception | None = None
        for attempt in range(retries + 1):
            conn = self._connection(parts.scheme, host, port)
            try:
                conn.request(method, target, body=body, headers=hdrs)
                raw = conn.getresponse()
                data = raw.read()
                return Response(raw.status, raw.headers, data)
            except (http.client.HTTPException, OSError, ssl.SSLError) as exc:
                last = exc
                self._drop(parts.scheme, host, port)
                if attempt >= retries:
                    break
        raise RegistryError(f"{method} {url} failed: {last}")

    def get(self, url: str, *, headers: dict[str, str] | None = None) -> Response:
        return self.request("GET", url, headers=headers)

    def get_json(self, url: str, *, headers: dict[str, str] | None = None) -> Any:
        resp = self.get(url, headers=headers)
        if not resp.ok:
            raise RegistryError(f"GET {url} returned {resp.status}")
        try:
            return resp.json()
        except ValueError as exc:
            raise RegistryError(f"GET {url} returned invalid JSON: {exc}") from exc

    def get_bytes(self, url: str, *, headers: dict[str, str] | None = None) -> bytes:
        resp = self.get(url, headers=headers)
        if not resp.ok:
            raise RegistryError(f"GET {url} returned {resp.status}")
        return resp.body

    def post_json(self, url: str, payload: Any, *, headers: dict[str, str] | None = None) -> Response:
        body = json.dumps(payload).encode("utf-8")
        hdrs = dict(_JSON)
        hdrs["Content-Length"] = str(len(body))
        if headers:
            hdrs.update(headers)
        return self.request("POST", url, headers=hdrs, body=body, retries=1)

    def close(self) -> None:
        pool = getattr(self._tls, "pool", None)
        if pool:
            for conn in pool.values():
                try:
                    conn.close()
                except OSError:
                    pass
            pool.clear()

    def __enter__(self) -> "HttpClient":
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()
