---
description: Run bug-hunter, silent-failure-scanner, and test-analyzer on a PR diff in parallel
---
Spawn the three pr-checks sub-agents in parallel against $ARGUMENTS (PR number, sha range, or current branch when empty), then consolidate their findings into a single report.

## Steps

1. **Resolve the diff scope** the same way `/code-review` does (PR via gh, sha range via git diff, or current branch's diff vs main).

2. **Spawn three Agent calls in a single message** so they run concurrently:
   - `subagent_type: "bug-hunter"` — concrete defects
   - `subagent_type: "silent-failure-scanner"` — swallowed errors
   - `subagent_type: "test-analyzer"` — test coverage / quality

   Pass the diff scope to each agent. Don't pre-summarise the diff for them — they read it themselves to keep their judgments independent.

3. **Wait for all three** to return. Treat their outputs as independent signals; do not let one agent's "looks fine" blunt another's defect report.

4. **Deduplicate.** When two agents flagged the same line for the same reason, merge into one entry. When two agents disagree (one flagged, one didn't), keep the flag and note the disagreement — the parent reviewer should see it.

5. **Sort by severity**: defects (bug-hunter) > silent failures > test gaps > test quality issues.

## Output format

```
SUMMARY: <one sentence on diff scope + size>

DEFECTS (<count>):
  <file:line>  <headline>
  ...

SILENT FAILURES (<count>):
  ...

TEST GAPS (<count>):
  ...

TEST QUALITY (<count>):
  ...

VERDICT: SHIP | NEEDS CHANGES | NEEDS TEST WORK
```

`SHIP` when all three agents returned zero high-confidence issues. `NEEDS CHANGES` when bug-hunter or silent-failure-scanner flagged anything. `NEEDS TEST WORK` when test-analyzer is the only flagger.

If $ARGUMENTS contained `--comment <PR>`, post the consolidated report as a single PR review comment. Do NOT post per-agent reports separately — three comment threads on the same PR is review noise.
