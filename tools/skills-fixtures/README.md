# skills-fixtures — minimal SKILL.md examples

Six example skills that exercise every shape `apps/cli/biu/internal/skills`'s loader and the runtime catalogue need to handle. Used by:

- `apps/cli/biu/internal/skills` parser tests (positive + negative paths)
- Future `services/runtime/internal/skills` install/import tests (PS2.3)
- Manual smoke against `biu skill install <fixture-dir>` once PS1.4 lands

Layout — one folder per fixture, each containing a `SKILL.md` and (where relevant) bundled resources:

| folder | what it exercises |
|---|---|
| [`minimal/`](./minimal/SKILL.md) | absolute floor: `name` + `description` only, no body extras |
| [`with-paths/`](./with-paths/SKILL.md) | `paths:` glob list — auto-attach matcher hits this in repo trees |
| [`with-resources/`](./with-resources/SKILL.md) | bundled `references/` file referenced from the body via `$RESOURCE_PATH` style hint |
| [`with-permissions/`](./with-permissions/SKILL.md) | declared `permissions:` (`sandbox.exec`, `network.fetch`); Cedar translation in PS3.4 reads these |
| [`update-of-target/`](./update-of-target/SKILL.md) | a "v1" skill that update_of-style proposals can target; pairs with the one below |
| [`broken-frontmatter/`](./broken-frontmatter/SKILL.md) | malformed YAML — loader falls back to the directory name as the identifier; body is whatever survived parsing. Pins the "tolerate, don't abort" contract |

## How the loader handles them

The loader (`apps/cli/biu/internal/skills/skills.go:Load`) walks `~/.biumind/skills/<n>/SKILL.md` then `<cwd>/.biumind/skills/<n>/SKILL.md` (project overrides user). Per-file:

1. Reads frontmatter (between `---` fences) into a flat map.
2. Splits into known fields (`name` / `description` / `when-to-use` / `user-invocable` / `paths`) plus an opaque pass-through.
3. Body trims leading/trailing whitespace; `$ARGS` and `$1` are substituted at invocation time, not load time.

`broken-frontmatter` exercises the loader's tolerance: a missing closing `---` fence means the parsed frontmatter map is empty, so `name` falls back to the directory name and the body comes through whatever was after the (unclosed) opening fence. The skill is *loadable* but missing its declared metadata. Tests assert this rather than expecting an error — the loader's contract is "best effort, keep the rest of the directory loadable".

## Re-using these fixtures

Tests typically symlink or copy the fixture root into a temp dir to avoid race-y mutations of the shared tree. Don't `chmod` the files — keep them readable and immutable.
