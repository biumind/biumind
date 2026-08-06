"""HubClient — Anthropic-compatible relay client.

Hub speaks the Anthropic Messages API (`POST /v1/messages`). Models are
resolved server-side based on the request's ``model`` field, so callers
don't pick the upstream provider explicitly — claude-* goes to
Anthropic, gpt-* goes to OpenAI, etc.

Two methods:

  - ``messages(...)``      — blocking; returns the parsed JSON response.
  - ``messages_stream(...)`` — generator yielding text deltas as they
                                arrive over SSE.
"""

from __future__ import annotations

import json
from typing import Any, Dict, Iterator, List, Optional

from .config import BiuMindConfig
from ._http import request_json, request_stream


class HubClient:
    def __init__(self, config: BiuMindConfig) -> None:
        self._cfg = config

    def messages(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        system: Optional[str] = None,
        max_tokens: int = 1024,
        extra: Optional[Dict[str, Any]] = None,
    ) -> Dict[str, Any]:
        """Send a non-streaming request. Returns the full response JSON."""
        body = self._body(model, messages, system, max_tokens, stream=False, extra=extra)
        _, payload = request_json(
            "POST",
            f"{self._cfg.hub_url}/v1/messages",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
            body=body,
        )
        return payload

    def messages_stream(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        system: Optional[str] = None,
        max_tokens: int = 1024,
        extra: Optional[Dict[str, Any]] = None,
    ) -> Iterator[str]:
        """Stream an Anthropic-style response, yielding text deltas only.

        SSE event types other than ``content_block_delta`` (errors,
        message_start, etc.) are dropped — callers needing the full
        event stream should call ``raw_stream`` instead.
        """
        body = self._body(model, messages, system, max_tokens, stream=True, extra=extra)
        for raw in request_stream(
            "POST",
            f"{self._cfg.hub_url}/v1/messages",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
            body=body,
        ):
            line = raw.decode("utf-8", errors="replace").strip()
            if not line.startswith("data:"):
                continue
            data = line[len("data:"):].strip()
            if not data or data == "[DONE]":
                continue
            try:
                event = json.loads(data)
            except json.JSONDecodeError:
                continue
            if event.get("type") == "content_block_delta":
                delta = event.get("delta", {})
                if delta.get("type") == "text_delta":
                    yield delta.get("text", "")

    def raw_stream(
        self,
        *,
        model: str,
        messages: List[Dict[str, Any]],
        system: Optional[str] = None,
        max_tokens: int = 1024,
        extra: Optional[Dict[str, Any]] = None,
    ) -> Iterator[Dict[str, Any]]:
        """Yield each parsed SSE event dict (full upstream shape)."""
        body = self._body(model, messages, system, max_tokens, stream=True, extra=extra)
        for raw in request_stream(
            "POST",
            f"{self._cfg.hub_url}/v1/messages",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
            body=body,
        ):
            line = raw.decode("utf-8", errors="replace").strip()
            if not line.startswith("data:"):
                continue
            data = line[len("data:"):].strip()
            if not data or data == "[DONE]":
                continue
            try:
                yield json.loads(data)
            except json.JSONDecodeError:
                continue

    @staticmethod
    def _body(
        model: str,
        messages: List[Dict[str, Any]],
        system: Optional[str],
        max_tokens: int,
        *,
        stream: bool,
        extra: Optional[Dict[str, Any]],
    ) -> Dict[str, Any]:
        body: Dict[str, Any] = {
            "model": model,
            "messages": messages,
            "max_tokens": max_tokens,
            "stream": stream,
        }
        if system:
            body["system"] = system
        if extra:
            body.update(extra)
        return body
