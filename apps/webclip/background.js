// BiuMind Clipper — background service worker.
//
// Only job in P2-F: install + own the right-click "save selection"
// context menu. Clicking the menu item runs the same selection
// extractor + clip POST that popup.js uses, but without opening the
// popup — useful for clipping a snippet without breaking flow.
//
// MV3 service workers are short-lived; we re-register the menu on
// onInstalled (extension install / browser launch) so it's always
// present even after a worker eviction. The actual click handler
// runs each time a click event fires, so per-event lifetime is fine.

const MENU_ID = "biumind-clip-selection";

chrome.runtime.onInstalled.addListener(() => {
  // chrome.contextMenus.create throws if the id already exists; the
  // upgrade path always wipes prior entries first so we never collide.
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({
      id: MENU_ID,
      title: "保存选中文字到 BiuMind",
      contexts: ["selection"],
    });
  });
});

chrome.contextMenus.onClicked.addListener((info, tab) => {
  if (info.menuItemId !== MENU_ID || !tab?.id) return;
  void clipSelection(tab);
});

async function clipSelection(tab) {
  const cfg = await chrome.storage.local.get([
    "server_url",
    "jwt_token",
    "last_project_id",
    "auto_ingest",
  ]);
  const serverUrl = (cfg.server_url || "").replace(/\/$/, "");
  const jwt = cfg.jwt_token || "";
  const projectId = cfg.last_project_id || "";
  const autoIngest = !!cfg.auto_ingest;
  if (!serverUrl || !jwt) {
    notify("BiuMind Clipper", "请先在设置中配置服务器地址和登录令牌");
    return;
  }
  if (!projectId) {
    notify(
      "BiuMind Clipper",
      "请先打开扩展弹窗选择一次项目（会被记住作为右键默认目标）",
    );
    return;
  }

  try {
    // Inject Turndown so the in-tab function can run.
    await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      files: ["vendor/turndown.js"],
    });
    const results = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: extractSelection,
    });
    const r = results?.[0]?.result;
    if (!r || r.error) {
      notify("BiuMind Clipper", r?.error || "未选中任何内容");
      return;
    }
    if (!r.markdown) {
      notify("BiuMind Clipper", "未选中任何内容");
      return;
    }

    const resp = await fetch(
      `${serverUrl}/v1/wiki/projects/${projectId}/sources/clip`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: "Bearer " + jwt,
        },
        body: JSON.stringify({
          url: tab.url || "",
          title: r.title || tab.title || "Untitled",
          content_md: r.markdown,
          metadata: { via: "webclip", extractor: "selection" },
        }),
      },
    );
    if (resp.status === 401 || resp.status === 403) {
      notify("BiuMind Clipper", "登录已过期，请重新设置令牌");
      return;
    }
    if (!resp.ok) {
      const text = await resp.text();
      notify("BiuMind Clipper",
        `保存失败 (${resp.status})：${text.slice(0, 100)}`);
      return;
    }
    const data = await resp.json();
    const sourceId = data.source_id || "";
    const shortId = sourceId.slice(0, 8);
    if (data.duplicate) {
      notify("BiuMind Clipper", `已存在重复源 ${shortId}`);
      return;
    }
    if (!autoIngest) {
      notify("BiuMind Clipper", `已保存：${shortId}`);
      return;
    }
    // Chain into ingest. P2-B: worker resolves source_id reverse-style
    // via brain's internal endpoint, so we send only the source_id +
    // title — content stays in brain.sources where the clip just put it.
    try {
      const ingestResp = await fetch(
        `${serverUrl}/v1/wiki/projects/${projectId}/ingest`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: "Bearer " + jwt,
          },
          body: JSON.stringify({
            source_id: sourceId,
            title: r.title || tab.title || "",
          }),
        },
      );
      if (!ingestResp.ok) {
        const text = await ingestResp.text();
        notify("BiuMind Clipper",
          `已保存 ${shortId}，但 ingest 启动失败 (${ingestResp.status}): ${text.slice(0, 80)}`);
        return;
      }
      const ingest = await ingestResp.json();
      const taskShort = (ingest.id || "").slice(0, 8);
      notify("BiuMind Clipper",
        `已保存 ${shortId} → ingest 任务 ${taskShort} 启动`);
    } catch (e) {
      notify("BiuMind Clipper",
        `已保存 ${shortId}，但 ingest 启动失败：${e?.message ?? e}`);
    }
  } catch (e) {
    notify("BiuMind Clipper", String(e?.message ?? e));
  }
}

// Tab-side selection→markdown — inlined because chrome.scripting
// doesn't carry closures across the popup→tab boundary.
function extractSelection() {
  try {
    // eslint-disable-next-line no-undef
    const td = new TurndownService({
      headingStyle: "atx",
      codeBlockStyle: "fenced",
    });
    td.remove(["script", "style", "noscript", "iframe", "form"]);

    // eslint-disable-next-line no-undef
    const sel = window.getSelection ? window.getSelection() : null;
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) {
      return { markdown: "", title: "" };
    }
    // eslint-disable-next-line no-undef
    const div = document.createElement("div");
    for (let i = 0; i < sel.rangeCount; i++) {
      div.appendChild(sel.getRangeAt(i).cloneContents());
    }
    return {
      markdown: td.turndown(div.innerHTML).trim(),
      // eslint-disable-next-line no-undef
      title: document.title || "",
    };
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
}

function notify(title, message) {
  // notifications permission is in manifest; on systems where the user
  // has disabled the OS-level Chrome notification toggle the call no-ops
  // silently — that's fine, we don't have a fallback channel from the
  // service worker.
  if (!chrome.notifications) return;
  chrome.notifications.create({
    type: "basic",
    iconUrl: chrome.runtime.getURL("icon128.png"),
    title,
    message,
  });
}
