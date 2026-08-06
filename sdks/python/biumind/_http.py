"""Stdlib-only HTTP helpers shared by HubClient and MemoryClient.

Why urllib instead of requests/httpx?
    The SDK has no third-party runtime dependencies, so users in
    air-gapped environments / locked-down CI matrices can install it
    without internet access and without conflicting with their pinned
    requests version. Streaming uses the raw HTTPResponse object so we
    can iterate SSE chunks line-by-line without buffering the body.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, Iterator, Optional, Tuple

from .errors import from_status


def request_json(
    method: str,
    url: str,
    *,
    token: str,
    timeout: float,
    body: Optional[Any] = None,
    query: Optional[Dict[str, str]] = None,
) -> Tuple[int, Dict[str, Any]]:
    """Issue a JSON request and decode the response body.

    Returns ``(status, parsed_body)``. Raises a typed BiuMindError for
    4xx / 5xx so callers don't have to check status manually.
    """
    full_url = _with_query(url, query)
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
    }
    data: Optional[bytes] = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(full_url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
            status = resp.status
    except urllib.error.HTTPError as e:
        body_text = e.read().decode("utf-8", errors="replace")
        retry_after = _retry_after(dict(e.headers or {}))
        raise from_status(e.code, body_text, retry_after=retry_after) from None
    return status, (json.loads(raw) if raw else {})


def request_stream(
    method: str,
    url: str,
    *,
    token: str,
    timeout: float,
    body: Optional[Any] = None,
) -> Iterator[bytes]:
    """Open an SSE / NDJSON stream. Yields raw line bytes (no '\\n').

    The caller is responsible for parsing line shape (Hub speaks SSE:
    ``data: {...}\\n\\n``). The stream is closed when the iterator is
    exhausted or the caller breaks out.
    """
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "text/event-stream",
    }
    data: Optional[bytes] = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=timeout)
    except urllib.error.HTTPError as e:
        body_text = e.read().decode("utf-8", errors="replace")
        retry_after = _retry_after(dict(e.headers or {}))
        raise from_status(e.code, body_text, retry_after=retry_after) from None
    try:
        for line in resp:
            yield line.rstrip(b"\r\n")
    finally:
        resp.close()


def _with_query(url: str, query: Optional[Dict[str, str]]) -> str:
    if not query:
        return url
    qs = urllib.parse.urlencode({k: v for k, v in query.items() if v is not None})
    sep = "&" if "?" in url else "?"
    return f"{url}{sep}{qs}"


def _retry_after(headers: Dict[str, Any]) -> float:
    raw = headers.get("Retry-After") or headers.get("retry-after")
    if raw is None:
        return 0.0
    try:
        return float(raw)
    except (TypeError, ValueError):
        return 0.0
