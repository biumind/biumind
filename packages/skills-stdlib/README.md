# skills-stdlib — bundled SKILL.md packages

The platform-shipped Skills the runtime auto-installs into every org as `source='bundled'`. Per the design doc (§16.2 of `BiuMind-Skills-Design.md`), these are *not* generic prompts (those go to marketplace) — they're "teach the LLM how to use BiuMind itself" packages.

## Layout

Each top-level folder is one skill, named with its identifier (kebab-case, matches `runtime.skills.identifier`):

```
skills-stdlib/
├── skill-creator/
│   └── SKILL.md
├── biumind/                 (planned — PS4.1)
│   └── SKILL.md
├── wiki/                    (planned — PS4.1)
└── …
```

## Loading

`services/runtime/internal/skills/builtin.go` (PS4.1) walks this tree at startup and upserts each skill into `runtime.skills` with `source='bundled', owner_id=NULL` (org-shared). Idempotent — re-running on a fresh DB or an existing one yields the same rows.

For now (PS3.3), `skill-creator` is the only one bundled. Other 7 land via PS4.1 once the loader exists.

## Authoring

Standard SKILL.md frontmatter applies (`name`, `description`, optional `paths`, `permissions`). Body is markdown. `$ARGS` substitution runs at invocation time.
