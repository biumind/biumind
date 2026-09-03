"""Fake mineru-api server（dev/e2e 用，非单测）。

实现 `wiki_parse/ocr.py` 依赖的最小契约：

    POST /tasks                  → {"task_id": "fake-<n>"}
    GET  /tasks/<id>             → {"status": "completed"}
    GET  /tasks/<id>/result      → {"results": {<name>: {"md_content": ...}}}

stdlib 单文件，零依赖。multipart body 不解析（直接收下）。
用法：python tests/fake_mineru.py [port]   （默认 8000）
容器内跑：podman run -d --name biu-mineru-mock --network biumind_biu-net \
    --network-alias mineru -v $PWD/tests/fake_mineru.py:/fake_mineru.py:ro \
    --entrypoint python docker.io/biumind/worker-wiki-parse:dev /fake_mineru.py
"""

from __future__ import annotations

import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MD_CONTENT = (
    "# 扫描件 OCR 模拟结果\n\n"
    "这是 fake MinerU 返回的 markdown 文本，用于 dev 端到端验证：\n"
    "worker → MinerU 契约 → parse-result 回写 → parse_meta.parser=mineru。\n"
)

_counter = 0


class _Handler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:  # noqa: N802 — stdlib 命名
        global _counter
        if self.path.rstrip("/") == "/tasks":
            # 读完 body 再响应，避免 client 端 BrokenPipe
            length = int(self.headers.get("Content-Length") or 0)
            remaining = length
            while remaining > 0:
                remaining -= len(self.rfile.read(min(remaining, 1 << 16)))
            _counter += 1
            self._json(200, {"task_id": f"fake-{_counter}"})
            return
        self._json(404, {"error": "not found"})

    def do_GET(self) -> None:  # noqa: N802 — stdlib 命名
        path = self.path.rstrip("/")
        if path.endswith("/result"):
            self._json(200, {"results": {"fake.pdf": {
                "md_content": MD_CONTENT, "images": {},
            }}})
            return
        if path.startswith("/tasks/"):
            self._json(200, {"status": "completed"})
            return
        self._json(404, {"error": "not found"})

    def _json(self, code: int, payload: dict) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt: str, *args) -> None:  # noqa: ANN001
        sys.stderr.write("fake-mineru: " + fmt % args + "\n")


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8000
    print(f"fake-mineru listening on :{port}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", port), _Handler).serve_forever()
