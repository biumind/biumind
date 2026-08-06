# {{slug}}

A view-only BiuMind App — no Go backend. The `list_recent` action
is forwarded by the runtime to the Wiki service via `wiki.read`.

## Develop

```bash
# Iterate on views without writing Go — actions resolve from fixtures/
biu app run --dev --mock fixtures/

# Once the manifest stabilises:
biu app validate
biu app pack
```

The desktop client polls `http://127.0.0.1:7099/v1/dev/apps` and
shows your App under "开发中" — edits to manifest.yaml or any
fixtures hot-reload automatically; press `r` in the terminal to
manually re-trigger.
