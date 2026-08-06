// Shared configuration for HubClient + MemoryClient. ``fromEnv()``
// builds it from BIUMIND_HUB_URL / BIUMIND_TOKEN / BIUMIND_BRAIN_URL.

export class BiuMindConfig {
  constructor({ hubUrl, token, brainUrl = "", timeoutMs = 30_000 }) {
    if (!hubUrl) throw new Error("hubUrl is required");
    if (!token) throw new Error("token is required");
    this.hubUrl = stripTrailingSlash(hubUrl);
    this.brainUrl = brainUrl ? stripTrailingSlash(brainUrl) : this.hubUrl;
    this.token = token;
    this.timeoutMs = timeoutMs;
  }

  static fromEnv(env = process.env) {
    return new BiuMindConfig({
      hubUrl: env.BIUMIND_HUB_URL || "",
      token: env.BIUMIND_TOKEN || "",
      brainUrl: env.BIUMIND_BRAIN_URL || "",
      timeoutMs: parseInt(env.BIUMIND_TIMEOUT_MS || "30000", 10),
    });
  }
}

function stripTrailingSlash(s) {
  return s.endsWith("/") ? s.slice(0, -1) : s;
}
