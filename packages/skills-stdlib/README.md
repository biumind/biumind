# skills-stdlib — bundled SKILL.md packages

The platform-shipped Skills the runtime auto-installs into every org as `source='bundled'`. Per the design doc (§16.2 of `BiuMind-Skills-Design.md`), these are *not* generic prompts (those go to marketplace) — they're "teach the LLM how to use BiuMind itself" packages.

## Layout

Each top-level folder is one skill, named with its identifier (kebab-case, matches `runtime.skills.identifier`):

```
skills-stdlib/
├── biumind/               platform map + task router (start here)
│   └── SKILL.md
├── wiki/                  knowledge base tools
│   └── SKILL.md
├── memory/                long-term memory tools
│   └── SKILL.md
├── graph/                 knowledge graph tools
│   └── SKILL.md
├── sandbox/               cloud workstation tools
│   └── SKILL.md
├── app-center/            BiuApp tools
│   └── SKILL.md
├── artifacts/             user-facing file exports
│   └── SKILL.md
└── skill-creator/         author new skills
    └── SKILL.md
```

## Loading

`services/runtime/internal/skills/builtin.go` walks this tree at startup and upserts each skill into `runtime.skills` with `source='bundled', owner_id=NULL` (org-shared). Idempotent — re-running on a fresh DB or an existing one yields the same rows.

All 8 skills are bundled; the loader ships with the runtime.

## Authoring

Standard SKILL.md frontmatter applies (`name`, `description`, optional `paths`, `permissions`). Body is markdown. `$ARGS` substitution runs at invocation time.
