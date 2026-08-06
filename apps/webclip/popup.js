// BiuMind Clipper — popup script.
//
// Flow:
//   1. Read { server_url, jwt_token } from chrome.storage.local
//   2. List the user's wiki projects → populate the picker
//   3. Inject Readability + Turndown into the active tab; extract the
//      page (or the user's text selection) as Markdown
//   4. On click → save to the chosen target: the wiki project
//      (POST /v1/wiki/projects/{pid}/sources/clip, surfacing the
//      source_id / duplicate flag) or the notes inbox
//      (POST /v1/notes). The target choice persists as last_target.
//
// The page extraction runs in the tab via chrome.scripting.executeScript;
// the popup itself stays sandboxed and never touches the page DOM
// directly. Vendor libs are loaded as files into the tab world rather
// than imported here so the popup keeps a clean global scope.

import { extractFromTab, extractSelectionFromTab } from "./extract.js";

const $ = (id) => document.getElementById(id);

const els = {
  status: $("status"),
  project: $("project"),
  projectRow: $("projectRow"),
  autoIngestRow: $("autoIngestRow"),
  targetRadios: document.querySelectorAll('input[name="target"]'),
  title: $("title"),
  preview: $("preview"),
  useSelection: $("useSelection"),
  autoIngest: $("autoIngest"),
  clipBtn: $("clipBtn"),
  optionsLink: $("optionsLink"),
  version: $("version"),
};

let state = {
  serverUrl: "",
  jwtToken: "",
  pageUrl: "",
  pageTitle: "",
  pageAuthor: "",
  markdown: "",
  selectionMarkdown: "",
  activeTabId: null,
  projects: [],
  // remembered across popup opens
  lastProjectId: "",
  target: "wiki", // "wiki" | "note"
};

els.optionsLink.addEventListener("click", (e) => {
  e.preventDefault();
  chrome.runtime.openOptionsPage();
});
els.version.textContent = "v" + chrome.runtime.getManifest().version;

els.useSelection.addEventListener("change", () => {
  refreshPreview();
});
els.autoIngest.addEventListener("change", () => {
  // Persist immediately so the user's choice carries to the next popup
  // open AND to the right-click background flow.
  void chrome.storage.local.set({ auto_ingest: els.autoIngest.checked });
});
els.clipBtn.addEventListener("click", () => {
  void clip();
});
els.project.addEventListener("change", () => {
  state.lastProjectId = els.project.value;
  void chrome.storage.local.set({ last_project_id: state.lastProjectId });
});
for (const radio of els.targetRadios) {
  radio.addEventListener("change", () => {
    if (!radio.checked) return;
    state.target = radio.value;
    void chrome.storage.local.set({ last_target: state.target });
    applyTarget();
  });
}

// Toggle wiki-only controls (project picker, auto-ingest) and
// re-evaluate whether the save button should be enabled for the
// current target. Safe to call before extraction finishes — it then
// only adjusts visibility.
function applyTarget() {
  const isNote = state.target === "note";
  els.projectRow.style.display = isNote ? "none" : "";
  // auto-ingest only applies to wiki sources; notes have no ingest step.
  els.autoIngestRow.style.display = isNote ? "none" : "";
  if (!state.markdown) return;
  if (isNote || state.projects.length > 0) {
    els.clipBtn.disabled = false;
    setStatus("ok", isNote ? "就绪 — 保存为笔记" : "就绪 — 选择项目并保存");
  } else {
    els.clipBtn.disabled = true;
    setStatus("warn", "请先在 BiuMind 创建一个 wiki 项目");
  }
}

// ── boot ─────────────────────────────────────────────────────────

void (async function main() {
  try {
    const cfg = await chrome.storage.local.get([
      "server_url", "jwt_token", "last_project_id", "auto_ingest", "last_target",
    ]);
    state.serverUrl = (cfg.server_url || "").replace(/\/$/, "");
    state.jwtToken = cfg.jwt_token || "";
    state.lastProjectId = cfg.last_project_id || "";
    // Default OFF the first time — the user opts in by ticking the box,
    // then the choice persists. Right-click flow consumes the same key
    // so the two surfaces stay in sync.
    els.autoIngest.checked = !!cfg.auto_ingest;

    // Restore the last save target; anything unexpected falls back to
    // the original wiki flow.
    state.target = cfg.last_target === "note" ? "note" : "wiki";
    const targetRadio = document.querySelector(
      `input[name="target"][value="${state.target}"]`,
    );
    if (targetRadio) targetRadio.checked = true;
    applyTarget();

    if (!state.serverUrl || !state.jwtToken) {
      setStatus("warn", "请先在「设置」配置服务器地址和登录令牌");
      return;
    }

    const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
    const tab = tabs[0];
    if (!tab) {
      setStatus("error", "无法定位当前标签页");
      return;
    }
    state.activeTabId = tab.id;
    state.pageUrl = tab.url || "";
    state.pageTitle = tab.title || "";
    els.title.value = state.pageTitle;

    // Run the two side-effects in parallel: project list (network) +
    // page extraction (script injection). Either may fail without
    // blocking the other.
    const [projects, content] = await Promise.allSettled([
      fetchProjects(),
      extractContent(tab.id),
    ]);

    if (projects.status === "fulfilled") {
      renderProjects(projects.value);
    } else {
      setStatus("error", "拉取项目失败：" + describeErr(projects.reason));
    }

    if (content.status === "fulfilled") {
      state.markdown = content.value.markdown || "";
      state.selectionMarkdown = content.value.selectionMarkdown || "";
      state.pageAuthor = content.value.articleAuthor || "";
      refreshPreview();
    } else {
      setStatus("error", "页面解析失败：" + describeErr(content.reason));
    }

    // Note target doesn't depend on the project list at all — as long
    // as we have markdown, the save button can go live.
    if (state.target === "note") {
      if (state.markdown) {
        els.clipBtn.disabled = false;
        setStatus("ok", "就绪 — 保存为笔记");
      }
    } else if (state.markdown && projects.status === "fulfilled" && state.projects.length > 0) {
      els.clipBtn.disabled = false;
      setStatus("ok", "就绪 — 选择项目并保存");
    } else if (state.projects.length === 0) {
      setStatus("warn", "请先在 BiuMind 创建一个 wiki 项目");
    }
  } catch (e) {
    setStatus("error", describeErr(e));
  }
})();

// ── helpers ──────────────────────────────────────────────────────

function setStatus(kind, msg) {
  els.status.className = "status " + kind;
  els.status.textContent = msg;
}

function describeErr(e) {
  if (!e) return "未知错误";
  if (e instanceof Error) return e.message;
  return String(e);
}

async function fetchProjects() {
  const resp = await fetch(state.serverUrl + "/v1/wiki/projects", {
    headers: authHeader(),
  });
  if (resp.status === 401 || resp.status === 403) {
    throw new Error("登录已过期，请到「设置」重新粘贴令牌");
  }
  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status}`);
  }
  const data = await resp.json();
  return Array.isArray(data.projects) ? data.projects : [];
}

function renderProjects(projects) {
  state.projects = projects;
  els.project.innerHTML = "";
  if (projects.length === 0) {
    const opt = document.createElement("option");
    opt.textContent = "（无项目）";
    els.project.appendChild(opt);
    return;
  }
  for (const p of projects) {
    const opt = document.createElement("option");
    opt.value = p.id;
    opt.textContent = p.name;
    els.project.appendChild(opt);
  }
  // Restore last-used selection if it's still in the list.
  if (state.lastProjectId && projects.some((p) => p.id === state.lastProjectId)) {
    els.project.value = state.lastProjectId;
  }
}

async function extractContent(tabId) {
  // Inject vendor files first; results land on `window.Readability` and
  // `window.TurndownService` in the tab's MAIN world. We then run our
  // own extractor function which uses both.
  await chrome.scripting.executeScript({
    target: { tabId },
    files: ["vendor/readability.js", "vendor/turndown.js"],
  });
  return extractFromTab(tabId);
}

function refreshPreview() {
  const md = els.useSelection.checked && state.selectionMarkdown
    ? state.selectionMarkdown
    : state.markdown;
  if (!md) {
    els.preview.textContent = "（无内容）";
    return;
  }
  els.preview.textContent = md.length > 500 ? md.slice(0, 500) + "…" : md;
}

function authHeader() {
  return { Authorization: "Bearer " + state.jwtToken };
}

async function clip() {
  els.clipBtn.disabled = true;
  setStatus("warn", "保存中…");
  if (state.target === "note") {
    await clipToNote();
    return;
  }
  const projectId = els.project.value;
  if (!projectId) {
    setStatus("error", "请选择一个项目");
    els.clipBtn.disabled = false;
    return;
  }
  let body;
  try {
    body = await buildClipBody();
  } catch (e) {
    setStatus("error", describeErr(e));
    els.clipBtn.disabled = false;
    return;
  }
  try {
    const resp = await fetch(
      `${state.serverUrl}/v1/wiki/projects/${projectId}/sources/clip`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json", ...authHeader() },
        body: JSON.stringify(body),
      },
    );
    if (resp.status === 401 || resp.status === 403) {
      setStatus("error", "登录已过期，请到「设置」重新粘贴令牌");
      els.clipBtn.disabled = false;
      return;
    }
    if (!resp.ok) {
      const text = await resp.text();
      setStatus("error", `保存失败 (${resp.status})：${text.slice(0, 160)}`);
      els.clipBtn.disabled = false;
      return;
    }
    const data = await resp.json();
    const sourceId = data.source_id || "";
    const shortId = sourceId.slice(0, 8);
    if (data.duplicate) {
      setStatus("ok", `已存在重复源 ${shortId}（同 URL+内容）`);
      // Don't auto-ingest duplicates: the original source presumably
      // already has wiki pages from its first ingest, and re-running
      // would emit duplicate pages that confuse the user. They can
      // still trigger ingest manually from the BiuMind UI if needed.
      return;
    }
    if (!els.autoIngest.checked) {
      setStatus("ok", `已保存：${shortId}`);
      return;
    }

    // Chain into ingest. As of P2-B the worker resolves source_id via
    // a brain reverse call, so we no longer inline content_md here —
    // saves bandwidth and keeps NATS messages small. The brain ingest
    // endpoint accepts source_id without raw_text starting from the
    // P2-B brain build.
    setStatus("warn", `已保存 ${shortId} — 正在创建 ingest 任务…`);
    try {
      const ingestResp = await fetch(
        `${state.serverUrl}/v1/wiki/projects/${projectId}/ingest`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json", ...authHeader() },
          body: JSON.stringify({
            source_id: sourceId,
            title: body.title,
          }),
        },
      );
      if (!ingestResp.ok) {
        const text = await ingestResp.text();
        setStatus(
          "warn",
          `已保存 ${shortId}，但 ingest 启动失败 (${ingestResp.status})：${text.slice(0, 120)}`,
        );
        return;
      }
      const ingest = await ingestResp.json();
      const taskShort = (ingest.id || "").slice(0, 8);
      setStatus("ok", `已保存 ${shortId} → ingest 任务 ${taskShort} 启动`);
    } catch (e) {
      setStatus("warn", `已保存 ${shortId}，但 ingest 启动失败：${describeErr(e)}`);
    }
  } catch (e) {
    setStatus("error", describeErr(e));
    els.clipBtn.disabled = false;
  }
}

// Save target "note": POST /v1/notes on the brain server. The request
// intentionally includes source_url/author up front — brain builds
// before the notes-api N3 fields simply ignore unknown JSON keys.
async function clipToNote() {
  let body;
  try {
    body = await buildNoteBody();
  } catch (e) {
    setStatus("error", describeErr(e));
    els.clipBtn.disabled = false;
    return;
  }
  try {
    const resp = await fetch(`${state.serverUrl}/v1/notes`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeader() },
      body: JSON.stringify(body),
    });
    if (resp.status === 401 || resp.status === 403) {
      setStatus("error", "登录已过期，请到「设置」重新粘贴令牌");
      els.clipBtn.disabled = false;
      return;
    }
    if (!resp.ok) {
      const text = await resp.text();
      setStatus("error", `保存失败 (${resp.status})：${text.slice(0, 160)}`);
      els.clipBtn.disabled = false;
      return;
    }
    // Success leaves the button disabled, matching the wiki flow —
    // re-clipping the same page is rarely what the user wants next.
    setStatus("ok", "已存到笔记");
  } catch (e) {
    setStatus("error", describeErr(e));
    els.clipBtn.disabled = false;
  }
}

// Shared markdown resolution for both save targets: honours the
// "selection only" checkbox and lazily re-extracts the selection if
// the initial page pass didn't capture one.
async function resolveMarkdown() {
  const useSel = els.useSelection.checked;
  let md = useSel ? state.selectionMarkdown : state.markdown;
  if (!md && useSel) {
    // User asked for selection but the page hasn't been re-extracted;
    // run the selection extractor now.
    const r = await extractSelectionFromTab(state.activeTabId);
    md = r.selectionMarkdown || "";
    state.selectionMarkdown = md;
  }
  if (!md) {
    throw new Error(useSel ? "未选中任何内容" : "页面无可保存内容");
  }
  return { md, useSel };
}

async function buildNoteBody() {
  const { md } = await resolveMarkdown();
  const body = {
    title: els.title.value || state.pageTitle || "Untitled",
    content_md: md,
    source_url: state.pageUrl,
  };
  // Author only exists when Readability produced a byline; selection
  // clips and fallback DOM dumps leave it out entirely.
  if (state.pageAuthor) {
    body.author = state.pageAuthor;
  }
  return body;
}

async function buildClipBody() {
  const { md, useSel } = await resolveMarkdown();
  return {
    url: state.pageUrl,
    title: els.title.value || state.pageTitle || "Untitled",
    content_md: md,
    metadata: {
      via: "webclip",
      extractor: useSel ? "selection" : "readability",
    },
  };
}
