# knowcode-editor-web

Milkdown WYSIWYG bundle embedded by the Knowcode Flutter client. One bundle, two transports:

- **Flutter Web** — served from `client/web/editor/`, embedded via `HtmlElementView` + iframe (same origin).
- **Native (macOS / iOS / Android)** — bundled into `client/assets/editor/`, served by `InAppLocalhostServer` and loaded into `flutter_inappwebview`.

The Flutter host and this bundle communicate via a versioned postMessage protocol (`src/bridge/protocol.ts`, mirrored in `client/lib/features/pages/editor_bridge_protocol.dart`).

## Layout

```
editor-web/
├── src/
│   ├── main.ts                 Crepe bootstrap + bridge wiring
│   ├── bridge/                 protocol.ts + client.ts (postMessage)
│   ├── markdown/               remark-stringify options (round-trip lock)
│   ├── plugins/                wikilink + mermaid (later phases)
│   └── theme/                  light.css + dark.css
├── scripts/sync-to-flutter.ts  copy dist/ → client/web/editor + client/assets/editor
├── tests/                      vitest (round-trip / bridge / wikilink)
└── index.html                  bundle entry HTML
```

## Workflow

```bash
npm install
npm run dev            # http://localhost:5174 — standalone editor for fast iteration
npm run build          # vite build + sync-to-flutter (writes both Flutter targets)
npm run test           # vitest
npm run typecheck      # tsc --noEmit
```

When working alongside Flutter:

```bash
make page-editor-dev   # starts Vite + flutter run -d chrome together (root Makefile)
```

## Bridge protocol (v1)

Every message is `{ type, v: 1, id?, payload }`. See `src/bridge/protocol.ts` for the source of truth and `client/lib/features/pages/editor_bridge_protocol.dart` for the matching Dart definitions — they must stay in lockstep.

## Round-trip stability

Milkdown reuses remark-stringify, which by default rewrites `*x*` → `_x_`, ` ``` ` → `~~~`, etc. We pin every option in `src/markdown/stringify-options.ts` so the bundle does not silently churn the user's source markdown. The vitest suite in `tests/round-trip.spec.ts` enforces this against a fixture corpus.
