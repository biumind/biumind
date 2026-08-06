// Page extraction helpers — run in the popup but execute their work
// inside the active tab via chrome.scripting.executeScript. The funcs
// passed to executeScript run in the tab's MAIN world where the
// vendor libs (Readability, TurndownService) have already been loaded
// by popup.js.
//
// IMPORTANT: chrome.scripting serializes the `func` body and runs it
// in the tab world; it does NOT carry closures. Helper functions used
// inside the injected function must be inlined into its body, hence
// the slight duplication between tabExtractor and tabSelectionExtractor.
// Keeping each injected function fully self-contained makes them
// portable across Chrome / Edge / Brave with no surprises.

export async function extractFromTab(tabId) {
  const results = await chrome.scripting.executeScript({
    target: { tabId },
    func: tabExtractor,
  });
  const r = results?.[0]?.result;
  if (!r) return { markdown: "", selectionMarkdown: "" };
  if (r.error) throw new Error(r.error);
  return r;
}

export async function extractSelectionFromTab(tabId) {
  const results = await chrome.scripting.executeScript({
    target: { tabId },
    func: tabSelectionExtractor,
  });
  const r = results?.[0]?.result;
  if (!r) return { selectionMarkdown: "" };
  if (r.error) throw new Error(r.error);
  return r;
}

// ── Functions injected into the tab world ────────────────────────

function tabExtractor() {
  // Inline turndown setup — chrome.scripting can't see outer-scope
  // helpers, so each injected function rebuilds what it needs.
  // eslint-disable-next-line no-undef
  const td = new TurndownService({
    headingStyle: "atx",
    codeBlockStyle: "fenced",
    bulletListMarker: "-",
    emDelimiter: "*",
  });
  td.remove(["script", "style", "noscript", "iframe", "form"]);

  try {
    // Whole-page extraction via Readability. Clone the document so
    // Readability's destructive parser doesn't mutate the live page.
    let articleHTML = "";
    let articleTitle = "";
    let articleAuthor = "";
    try {
      // eslint-disable-next-line no-undef
      const doc = document.cloneNode(true);
      // eslint-disable-next-line no-undef
      const reader = new Readability(doc);
      const parsed = reader.parse();
      if (parsed) {
        articleHTML = parsed.content || "";
        articleTitle = parsed.title || "";
        articleAuthor = parsed.byline || "";
      }
    } catch (_e) {
      // Readability fails on SPA / login walls; fall through to a
      // plain-DOM dump so we still produce something.
    }
    if (!articleHTML) {
      // eslint-disable-next-line no-undef
      articleHTML = document.body ? document.body.innerHTML : "";
    }
    const markdown = td.turndown(articleHTML).trim();

    // Selection — independent of article so the user can choose.
    let selectionMarkdown = "";
    // eslint-disable-next-line no-undef
    const sel = window.getSelection ? window.getSelection() : null;
    if (sel && sel.rangeCount > 0 && !sel.isCollapsed) {
      // eslint-disable-next-line no-undef
      const div = document.createElement("div");
      for (let i = 0; i < sel.rangeCount; i++) {
        div.appendChild(sel.getRangeAt(i).cloneContents());
      }
      selectionMarkdown = td.turndown(div.innerHTML).trim();
    }

    return { markdown, selectionMarkdown, articleTitle, articleAuthor };
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
}

function tabSelectionExtractor() {
  // eslint-disable-next-line no-undef
  const td = new TurndownService({
    headingStyle: "atx",
    codeBlockStyle: "fenced",
    bulletListMarker: "-",
    emDelimiter: "*",
  });
  td.remove(["script", "style", "noscript", "iframe", "form"]);

  try {
    // eslint-disable-next-line no-undef
    const sel = window.getSelection ? window.getSelection() : null;
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) {
      return { selectionMarkdown: "" };
    }
    // eslint-disable-next-line no-undef
    const div = document.createElement("div");
    for (let i = 0; i < sel.rangeCount; i++) {
      div.appendChild(sel.getRangeAt(i).cloneContents());
    }
    const selectionMarkdown = td.turndown(div.innerHTML).trim();
    return { selectionMarkdown };
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
}
