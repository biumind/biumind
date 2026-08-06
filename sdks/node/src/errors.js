// Typed exception hierarchy. Mirrors sdks/python/biumind/errors.py.

export class BiuMindError extends Error {
  constructor(message, { status = 0, body = "" } = {}) {
    super(message);
    this.name = "BiuMindError";
    this.status = status;
    this.body = body;
  }
}

export class AuthError extends BiuMindError {
  constructor(message, opts = {}) {
    super(message, opts);
    this.name = "AuthError";
  }
}

export class RateLimitError extends BiuMindError {
  constructor(message, { retryAfter = 0, ...opts } = {}) {
    super(message, opts);
    this.name = "RateLimitError";
    this.retryAfter = retryAfter;
  }
}

export class NotFoundError extends BiuMindError {
  constructor(message, opts = {}) {
    super(message, opts);
    this.name = "NotFoundError";
  }
}

export function fromStatus(status, body, retryAfter = 0) {
  const msg = `http ${status}: ${String(body).slice(0, 200)}`;
  if (status === 401 || status === 403) return new AuthError(msg, { status, body });
  if (status === 404) return new NotFoundError(msg, { status, body });
  if (status === 429) return new RateLimitError(msg, { status, body, retryAfter });
  return new BiuMindError(msg, { status, body });
}
