// Consolidation prompt builder.
//
// Three-phase structure:
//
//   1. Orient — list memory dir contents, read the entrypoint,
//      skim existing topic files
//   2. Gather — pull recent signal from session transcripts
//   3. Consolidate — synthesise into durable, deduplicated memories
//
// Kept biu-flavoured: no KAIROS / dream feature gates.

package autodream

import (
	"fmt"
	"strings"
)

// BuildConsolidationPrompt assembles the consolidation prompt the
// runner sends to the model.
//
// memoryDir is the absolute path to ~/.biumind/memory.
// transcriptsDir is where session jsonl files live.
// touchedSessions is the list of session file basenames that have
// been modified since the last consolidation — passed in so the
// prompt can scope grep narrowly without scanning the entire dir.
func BuildConsolidationPrompt(memoryDir, transcriptsDir string, touchedSessions []string) string {
	var b strings.Builder
	b.WriteString("# Auto-Dream: Memory Consolidation\n\n")
	b.WriteString(`You are performing a memory consolidation pass — a reflective sweep over the long-running memory directory. Synthesize what you've learned recently into durable, well-organized memories so that future sessions can orient quickly.

Memory directory: ` + "`" + memoryDir + "`" + `
Session transcripts: ` + "`" + transcriptsDir + "`" + ` (large JSONL files — grep narrowly, don't read whole files).

`)
	if len(touchedSessions) > 0 {
		b.WriteString("Sessions modified since last consolidation (priority for grep):\n")
		max := 20
		if len(touchedSessions) > max {
			fmt.Fprintf(&b, "  (showing %d of %d most recent)\n", max, len(touchedSessions))
			touchedSessions = touchedSessions[len(touchedSessions)-max:]
		}
		for _, s := range touchedSessions {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteByte('\n')
	}

	b.WriteString(`---

## Phase 1 — Orient

- ` + "`ls`" + ` the memory directory to see what already exists
- Read ` + "`MEMORY.md`" + ` to understand the current index (if present)
- Skim existing topic files so you improve them rather than creating duplicates
- If ` + "`logs/`" + ` or ` + "`sessions/`" + ` subdirectories exist, review recent entries there

## Phase 2 — Gather recent signal

Look for new information worth persisting. Sources, in priority order:

1. **Existing memories that drifted** — facts that contradict what you see in the codebase / transcripts now. Update or delete.
2. **Cross-session patterns** — the same kind of question / mistake / fix recurring in 2+ recent sessions is worth distilling.
3. **Transcript search** — for specific context (e.g. "what was yesterday's build error?"), grep the transcripts for narrow terms:
   ` + "`grep -rn \"<narrow term>\" " + transcriptsDir + "/ --include=\"*.jsonl\" | tail -50`" + `

## Phase 3 — Consolidate

Write back to the memory directory:

- **Update ` + "`MEMORY.md`" + `** to reflect new sections / archived sections.
- **Add or update topic files** for things worth carrying forward (architecture decisions, recurring bugs, conventions).
- **Delete or archive** memories that are no longer relevant (e.g. fixed bugs, deprecated APIs).
- **Consolidate duplicates** — if two memories cover the same ground, merge into one well-written paragraph.

## Constraints

- Be **decisive**: better to compress 5 vague notes into 1 sharp one than to keep all 5.
- Memory files are markdown; keep them human-readable.
- Don't rewrite the whole memory; touch only what needs change.
- When uncertain about a fact, **don't write it down**. False memories are worse than missing ones.
- Each topic file: ≤ 2KB. The dream is for distillation, not bulk archiving.

When finished, summarise WHAT changed (1-3 lines) and stop.`)

	return b.String()
}
