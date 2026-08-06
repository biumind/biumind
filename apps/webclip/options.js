// Settings page — only persists `server_url` and `jwt_token` to
// chrome.storage.local. Both popup and background read these on
// every clip; no in-memory cache to invalidate.

const $ = (id) => document.getElementById(id);

const els = {
  serverUrl: $("serverUrl"),
  jwtToken: $("jwtToken"),
  saveBtn: $("saveBtn"),
  testBtn: $("testBtn"),
  status: $("status"),
  autoIngestDefault: $("autoIngestDefault"),
};

void (async function load() {
  const cfg = await chrome.storage.local.get([
    "server_url", "jwt_token", "auto_ingest",
  ]);
  els.serverUrl.value = cfg.server_url || "";
  els.jwtToken.value = cfg.jwt_token || "";
  els.autoIngestDefault.checked = !!cfg.auto_ingest;
})();

// Persist the default toggle independently of the Save button so the
// user doesn't have to click anything else after toggling. The popup
// reads the same `auto_ingest` key on every open, so the change takes
// effect immediately on the next clip.
els.autoIngestDefault.addEventListener("change", () => {
  void chrome.storage.local.set({ auto_ingest: els.autoIngestDefault.checked });
});

els.saveBtn.addEventListener("click", async () => {
  const serverUrl = (els.serverUrl.value || "").trim().replace(/\/$/, "");
  const jwtToken = (els.jwtToken.value || "").trim();
  if (!serverUrl || !jwtToken) {
    setStatus("error", "请同时填写服务器地址和令牌");
    return;
  }
  if (!/^https?:\/\//.test(serverUrl)) {
    setStatus("error", "服务器地址必须以 http:// 或 https:// 开头");
    return;
  }
  await chrome.storage.local.set({
    server_url: serverUrl,
    jwt_token: jwtToken,
  });
  setStatus("ok", "已保存");
});

els.testBtn.addEventListener("click", async () => {
  const serverUrl = (els.serverUrl.value || "").trim().replace(/\/$/, "");
  const jwtToken = (els.jwtToken.value || "").trim();
  if (!serverUrl || !jwtToken) {
    setStatus("error", "请先填写服务器地址和令牌");
    return;
  }
  setStatus("warn", "测试中…");
  try {
    const resp = await fetch(serverUrl + "/v1/wiki/projects", {
      headers: { Authorization: "Bearer " + jwtToken },
    });
    if (resp.status === 401 || resp.status === 403) {
      setStatus("error", "令牌无效或已过期");
      return;
    }
    if (!resp.ok) {
      setStatus("error", `HTTP ${resp.status}`);
      return;
    }
    const data = await resp.json();
    const n = Array.isArray(data.projects) ? data.projects.length : 0;
    setStatus("ok", `连接正常 — 当前账号有 ${n} 个项目`);
  } catch (e) {
    setStatus("error", "请求失败：" + (e?.message || e));
  }
});

function setStatus(kind, msg) {
  els.status.style.display = "block";
  els.status.className = "status " + kind;
  els.status.textContent = msg;
}
