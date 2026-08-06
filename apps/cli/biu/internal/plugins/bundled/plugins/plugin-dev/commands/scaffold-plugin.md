---
description: Scaffold a new biu plugin directory with manifest + example component
---
Scaffold a new biu plugin under the directory specified in $ARGUMENTS (or the current directory if empty), then run `biu plugin validate` on the result.

## Steps

1. **Decide the plugin name**: ask the user if not obvious from $ARGUMENTS. Lowercase letters, digits, hyphens; 1–64 chars.

2. **Decide what the plugin contributes**: ask which of the five component types it should ship — commands, agents, skills, hooks, output-styles. Pick at least one. Multi-component plugins are fine.

3. **Create the directory layout** (only the components the user picked):

   ```
   <name>/
   ├── .claude-plugin/
   │   └── plugin.json
   ├── README.md
   ├── commands/<command>.md          ← when commands picked
   ├── agents/<agent>.md              ← when agents picked
   ├── skills/<skill>/SKILL.md        ← when skills picked
   ├── hooks/hooks.json               ← when hooks picked
   └── output-styles/<style>.md       ← when output-styles picked
   ```

4. **Write the manifest**:

   ```json
   {
     "name": "<name>",
     "version": "0.1.0",
     "description": "<one-line — ≤256 chars>",
     "author": { "name": "<author>", "email": "<optional>" }
   }
   ```

5. **Write a placeholder for each picked component**. Each placeholder is a working example the user edits — not a comment-only stub. Example for commands:

   ```
   ---
   description: <what the command does>
   ---
   <Prompt body. Use $ARGUMENTS for user-supplied args, $CWD for cwd, $DATE for date.>
   ```

6. **Add a README.md** explaining what the plugin does and how to install it (`biu plugin install <path>` and / or marketplace registration).

7. **Run `biu plugin validate <path>`** via Bash and surface the result.

8. **Tell the user the next steps**: edit the placeholder content, then `biu plugin install <path>` to test locally, or `biu plugin pack` (when shipping a `.biuplugin` archive) for distribution.
