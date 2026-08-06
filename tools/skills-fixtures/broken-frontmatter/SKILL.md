---
name: broken-frontmatter
description: "Intentionally malformed — frontmatter never closes (no trailing ---).
when-to-use: Loader test expects this file to be skipped with a stderr warning, not crash the whole load.

The body never gets parsed because there's no closing fence above. The
loader's contract is "best effort": the malformed file is dropped and
loading continues for sibling skills. See skills.Load + loadFile.
