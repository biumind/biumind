"""Smoke tests against an in-process HTTP stub.

These tests don't reach the network — they use stdlib's
``http.server`` to mock the Hub / Brain endpoints, so they run in CI
without provisioning a service.
"""

from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from biumind import (
    BiuMindConfig,
    HubClient,
    MemoryClient,
    RateLimitError,
    AuthError,
)


class _Handler(BaseHTTPRequestHandler):
    routes: dict = {}  # set per-test

    def log_message(self, *_a, **_k):  # silence stderr in tests
        pass

    def _dispatch(self, method: str) -> None:
        # Strip query string — tests match on path only.
        key = (method, self.path.split("?", 1)[0])
        h = self.routes.get(key)
        if not h:
            self.send_response(404)
            self.end_headers()
            self.wfile.write(b'{"error":"no route"}')
            return
        length = int(self.headers.get("Content-Length", "0") or 0)
        body = self.rfile.read(length).decode("utf-8") if length else ""
        h(self, body)

    def do_GET(self):    return self._dispatch("GET")
    def do_POST(self):   return self._dispatch("POST")
    def do_DELETE(self): return self._dispatch("DELETE")


class _Server:
    def __init__(self, routes):
        _Handler.routes = routes
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        self.port = self.httpd.server_address[1]
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    def close(self):
        self.httpd.shutdown()


def _ok(handler, body: dict) -> None:
    handler.send_response(200)
    handler.send_header("Content-Type", "application/json")
    handler.end_headers()
    handler.wfile.write(json.dumps(body).encode("utf-8"))


class HubClientTests(unittest.TestCase):
    def test_messages_blocking(self):
        def hub(handler, body):
            payload = json.loads(body)
            assert payload["model"] == "claude-test"
            _ok(handler, {"id": "msg_1", "content": [{"text": "hi"}]})

        srv = _Server({("POST", "/v1/messages"): hub})
        try:
            cfg = BiuMindConfig(hub_url=srv.url(), token="t")
            client = HubClient(cfg)
            resp = client.messages(model="claude-test",
                                   messages=[{"role": "user", "content": "x"}])
            self.assertEqual(resp["id"], "msg_1")
        finally:
            srv.close()

    def test_messages_stream_yields_text_deltas(self):
        sse = (
            "data: {\"type\":\"message_start\"}\n\n"
            "data: {\"type\":\"content_block_delta\","
            "\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\n"
            "data: {\"type\":\"content_block_delta\","
            "\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n"
            "data: [DONE]\n\n"
        )

        def hub(handler, _body):
            handler.send_response(200)
            handler.send_header("Content-Type", "text/event-stream")
            handler.end_headers()
            handler.wfile.write(sse.encode("utf-8"))

        srv = _Server({("POST", "/v1/messages"): hub})
        try:
            cfg = BiuMindConfig(hub_url=srv.url(), token="t")
            client = HubClient(cfg)
            chunks = list(client.messages_stream(
                model="m", messages=[{"role": "user", "content": "x"}]))
            self.assertEqual("".join(chunks), "hello")
        finally:
            srv.close()

    def test_rate_limit_error(self):
        def hub(handler, _body):
            handler.send_response(429)
            handler.send_header("Retry-After", "12")
            handler.end_headers()
            handler.wfile.write(b'{"error":"rpm"}')

        srv = _Server({("POST", "/v1/messages"): hub})
        try:
            cfg = BiuMindConfig(hub_url=srv.url(), token="t")
            client = HubClient(cfg)
            with self.assertRaises(RateLimitError) as cm:
                client.messages(model="m", messages=[])
            self.assertEqual(cm.exception.retry_after, 12.0)
        finally:
            srv.close()


class MemoryClientTests(unittest.TestCase):
    def test_store_and_recall(self):
        store_calls = []

        def store(handler, body):
            store_calls.append(json.loads(body))
            _ok(handler, {
                "id": "mem_1", "project_id": "p", "kind": "recall",
                "content": "hi", "salience": 0.5,
                "created_at": "2026-01-01T00:00:00Z",
                "last_accessed_at": "2026-01-01T00:00:00Z",
            })

        def recall(handler, _body):
            _ok(handler, {
                "memories": [{
                    "id": "mem_1", "project_id": "p", "kind": "recall",
                    "content": "hi", "salience": 0.5, "score": 1.23,
                    "created_at": "2026-01-01T00:00:00Z",
                    "last_accessed_at": "2026-01-01T00:00:00Z",
                }],
                "mode": "hybrid", "query": "hi",
            })

        srv = _Server({
            ("POST", "/v1/memory"): store,
            ("GET", "/v1/memory/recall"): recall,
        })
        try:
            cfg = BiuMindConfig(hub_url=srv.url(), token="t")
            client = MemoryClient(cfg)
            m = client.store(project_id="p", content="hi")
            self.assertEqual(m.id, "mem_1")
            self.assertEqual(store_calls[0]["project_id"], "p")
            r = client.recall(project_id="p", q="hi")
            self.assertEqual(r.mode, "hybrid")
            self.assertEqual(r.memories[0].score, 1.23)
        finally:
            srv.close()

    def test_invalid_kind_raises(self):
        cfg = BiuMindConfig(hub_url="http://localhost", token="t")
        client = MemoryClient(cfg)
        with self.assertRaises(ValueError):
            client.store(project_id="p", content="x", kind="garbage")

    def test_auth_error(self):
        def hub(handler, _body):
            handler.send_response(401)
            handler.end_headers()
            handler.wfile.write(b'{"error":"bad token"}')

        srv = _Server({("GET", "/v1/memory"): hub})
        try:
            cfg = BiuMindConfig(hub_url=srv.url(), token="t")
            client = MemoryClient(cfg)
            with self.assertRaises(AuthError):
                client.list(project_id="p")
        finally:
            srv.close()


if __name__ == "__main__":
    unittest.main()
