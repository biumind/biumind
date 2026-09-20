# Skills User Guide

A Skill is an instruction package that extends what your AI assistant can do: a well-written playbook of steps and key points, optionally accompanied by scripts, reference material, and other resources. Once installed, the assistant automatically invokes the right skill when the situation calls for it — recurring routines like "write the weekly report", "generate an architecture diagram", or "write Git commit messages to the team's convention" can all be captured as a skill instead of re-taught every time.

The entry point is the **Skills** page in the client. Sign in first — otherwise the page prompts "Please sign in to manage cloud skills".

## Browsing and filtering

The skills list is a single column of cards. Each row shows: an avatar (an icon or initial, colored automatically from the skill's identifier), name, source badge, one-line description, status text, and a "⋯" menu on the right. Built-in skills are pinned to the front; the rest sort by update time, newest first.

The filter tabs at the top (with counts):

| Filter | Meaning |
|---|---|
| All | Every skill |
| Built-in | Shipped by BiuMind |
| Organization | Shared with you by other members of your team |
| Mine | Created or imported by you |
| Marketplace | Third-party skills installed from the skill store / a URL |
| Pending review | Submitted drafts awaiting approval to go live |

There are four states: **Enabled** (usable by the assistant), **Pending review** (draft or update proposal), **Disabled** (manually turned off), and **Suspended** (suspended by the platform).

## Viewing a skill's details

Click a skill row to open the detail panel — a right-hand drawer on desktop (your list context stays put), full-screen on narrow displays. It has two tabs:

**Overview**:

- **What this skill can access**: a plain-language rendering of the permissions — for example "can read your knowledge base", "can run code in a sandbox", "can access the external network"; with no extra permissions it shows "No extra permissions (invokes the model only)"
- **Details**: author, version, license, repository link, update time, plus **invocation count** and **last invoked** — you can tell at a glance which skills earn their keep
- **Third-party notice**: non-built-in skills are marked "This skill is provided by a third party. BiuMind does not guarantee it behaves as expected — evaluate before use"
- **Auto-attach paths** (if any): the working directories where the skill mounts automatically
- **Based on an existing version** (if any): if this is an update proposal for an existing skill, summaries of the old and new versions are shown side by side
- **Action buttons**: pending-review skills show **Approve** and **Reject** (rejection requires a reason); non-built-in skills show **Delete** (with confirmation — irreversible)

**Skill contents**:

The left side is the skill's file tree — `SKILL.md` (the skill's playbook itself) plus resource files like `scripts/`, `references/`, and `assets/`; click any file to view it on the right. Resource files show their size, type, and checksum. Some large files live in cloud object storage, and the detail page shows only their metadata for now.

## Installing skills

Click **Add** in the top-right corner to open the install dialog; the three methods map to the three segments at the top:

| Method | How | Best for |
|---|---|---|
| URL | Paste a `https://…/SKILL.md` address; the server fetches it over HTTPS | Installing from the skill store or a link someone shared (the **Skill Store** button goes straight to this tab) |
| Zip | Pick a local `.biuskill` / `.zip` skill package to upload (≤ 8 MB) | Skill packages you obtained offline |
| Write your own | Fill in an identifier (kebab-case), name, description, and body — and create your own skill on the spot | Capturing your own recurring routines as skills |

> [!TIP]
> In a hand-written skill body you can use the `$ARGS` placeholder: arguments passed at invocation get substituted into it, so one skill can absorb varying inputs.

Failed installs show the reason inline (package over the limit, URL fetch failure, etc.) — fix it and retry right there.

## Enable, disable, and pin

The "⋯" menu on a skill row offers different actions depending on its state:

- **Enabled**: **Pin to default assistant**, **Disable**
- **Disabled**: **Enable**
- **Pending review**: **Approve** (approved skills are enabled for the default assistant automatically), **Reject**
- All non-built-in skills: **Delete**

Enabling, disabling, and pinning act on your **default assistant**: a disabled skill is no longer auto-loaded by the assistant, but it stays in the list ready to re-enable; built-in skills can't be deleted, but can be disabled the same way.

## Cloud sync and team collaboration

Skills all live under your cloud account — sign in on another device and the same list is there, no manual migration. The list updates live across devices:

- Install, enable, disable, or delete a skill on another device, and the list on this one refreshes automatically
- In team scenarios — someone submits a new skill or an update, an admin approves or rejects it, someone shares a skill with the organization — the relevant members see a live notice (like "skill-creator · approved")
- Submitted proposals (including updates to existing skills) appear in the list as "Pending review" and take effect only after the approve / reject flow, so unvetted instructions never land directly in everyone's assistant

> [!WARNING]
> Third-party skills are, at heart, instructions to your assistant — they may ask to access your knowledge base, your files, or even sandbox execution. Before installing, read the "What this skill can access" list on the detail page carefully, and install only what you need.

## Want to write your own skills?

This guide covers the usage side only: browsing, installing, enabling, and approvals. For the skill file format (the `SKILL.md` frontmatter and body conventions), permission declarations, the commands for packaging a `.biuskill`, and the full submit-review lifecycle, see the developer documentation, [Developing and using Skills](../developers/skills.md).
