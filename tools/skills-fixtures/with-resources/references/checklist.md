# Skill checklist

A reference resource that lives under the `references/` directory of a
skill bundle. Loaded on demand by `skill.read_reference` (server side)
or inlined into the skill row's `resources` map at install time.

- [ ] frontmatter has `name` + `description`
- [ ] body uses `$ARGS` for runtime substitution
- [ ] resources/ files are referenced by relative path
- [ ] permissions declared if the skill executes commands
