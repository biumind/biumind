# {{slug}}

A minimal BiuMind App. Backend-only — single action, no UI surface.

## Develop

```bash
biu app validate                       # check manifest
biu app inspect                        # see resolved manifest
biu app pack                           # bundle into .biuapp
```

## Implement

The Go side lives wherever you want; the CLI only manages
manifest + bundle. Wire your `App` implementation into the
`app_center` registry with `Register(ctx, &MyApp{})`.

See `docs/BiuMind-AppCenter-DevGuide.md` for the full SDK API.
