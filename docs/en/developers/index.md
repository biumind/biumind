# Developers

Every integration surface BiuMind offers: one account, one JWT, six entry points — pick the one that fits your scenario.

## Quick chooser

| I want to… | Use | Docs |
|---|---|---|
| Let any AI agent (Claude Code, other MCP clients) read and write my knowledge base | **MCP tool service** (recommended, zero SDK dependencies) | [API reference: MCP](api.md) |
| Call knowledge base / memory / model / generation APIs from my own service | **REST API** | [API reference: REST](api.md) |
| Embed a BiuMind client in Go / Python / Node | **Public SDKs** (Apache-2.0) | [SDKs (Go / Python / Node)](sdks.md) |
| Build my own AI workbench UI on top of BiuMind's agent runtime | **SDK Protocol** (WebSocket bidirectional stream) | [SDK Protocol](sdk-protocol.md) |
| Write an app that can be published to the App Center | **BiuApp** | [BiuApp development](biuapp.md) |
| Write reusable, signable skills for agents | **Skills** | [Skill development](skills.md) |

## Authentication model

All endpoints share a single origin and a single credential: generate a PAT under "Settings → Virtual API Key" in the client, and call with it as a Bearer JWT. See [API reference](api.md) for details.

## Licensing

`sdks/` and `extensions/` are Apache-2.0; the platform itself (`apps/`, `services/`, `workers/`, `packages/`) is under the BiuMind Community License (source-available; offering it to third parties as a public SaaS is prohibited) — your own BiuApp / skill code belongs to you.
