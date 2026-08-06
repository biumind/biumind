"""Typed exception hierarchy for the BiuMind SDK.

Every HTTP error from Hub or Brain is mapped to one of these so callers
can do ``except RateLimitError`` instead of inspecting status codes.
"""

from __future__ import annotations


class BiuMindError(Exception):
    """Base error. All SDK-raised exceptions inherit from this."""

    def __init__(self, message: str, *, status: int = 0, body: str = "") -> None:
        super().__init__(message)
        self.status = status
        self.body = body


class AuthError(BiuMindError):
    """401 / 403 — bearer token missing, invalid, or insufficient scope."""


class RateLimitError(BiuMindError):
    """429 — quota exceeded. ``retry_after`` is set when the server hints one."""

    def __init__(self, message: str, *, status: int = 429,
                 body: str = "", retry_after: float = 0.0) -> None:
        super().__init__(message, status=status, body=body)
        self.retry_after = retry_after


class NotFoundError(BiuMindError):
    """404 — resource id does not exist or caller has no access."""


def from_status(status: int, body: str, retry_after: float = 0.0) -> BiuMindError:
    """Map an HTTP status to the most specific SDK error."""
    msg = f"http {status}: {body[:200]}"
    if status in (401, 403):
        return AuthError(msg, status=status, body=body)
    if status == 404:
        return NotFoundError(msg, status=status, body=body)
    if status == 429:
        return RateLimitError(msg, status=status, body=body, retry_after=retry_after)
    return BiuMindError(msg, status=status, body=body)
