---
name: sandbox
display_name: 云沙盒
description: 在 gVisor / Firecracker 隔离环境中执行代码、运行命令并管理文件。当用户需要运行代码、执行脚本或处理隔离任务时使用。
icon: 📦
permissions: ["sandbox.exec"]
---

# BiuMind Sandbox tooling

Sandboxes are ephemeral, isolated cloud workstations: gVisor (free
tier) or Firecracker MicroVM (Pro). Use them for ANY code execution
that's risky, long-running, or needs scratch disk the user
shouldn't keep.

## Tier choice

  cloud_free     0.5 CPU / 512Mi / 10Gi tmpfs / 30 min idle TTL
                 Fine for: scripts, light data processing, lint runs
  cloud_pro      2 CPU / 2Gi / 50Gi PVC / pause/resume / snapshot
                 Use for: long-running training, persistent dev env
  enterprise     Custom — Firecracker microVM + private VPC

Pick cloud_free unless the user explicitly needs persistence.

## API

  POST   /v1/sandboxes                       create — returns id + topic
  POST   /v1/sandboxes/{id}/exec             SSE-streamed argv exec
  POST   /v1/sandboxes/{id}/pause            freeze (pro+)
  POST   /v1/sandboxes/{id}/resume           thaw (pro+)
  POST   /v1/sandboxes/{id}/snapshot         commit rootfs → image (pro+)
  DELETE /v1/sandboxes/{id}                  destroy

## Skill-bound exec (preferred over raw bash)

When you have a skill with bundled scripts, prefer
skill.exec_script — it auto-mounts /skill/<vpath> for resources
the SKILL.md references. The runtime tool layer also gates on
Cedar policy (the skill must declare sandbox.exec permission).

For ad-hoc commands without skill resources, the regular bash tool
is faster (no skill lookup overhead).

## Output stream format

`/v1/sandboxes/{id}/exec` returns SSE:

  data: <stdout/stderr line>
  data: <stdout/stderr line>
  ...
  event: end
  data: {"exit_code": 0}

Heartbeats arrive as `: heartbeat\n\n` every 15s — drop them.

## Don't

  - Don't put secrets in argv — they're visible in logs. Use
    stdin (req.stdin field, base64-encoded) or env vars.
  - Don't expect filesystem persistence on cloud_free past 30 min
    idle. Use Files (skill.export_file) for anything the user
    should keep.
  - Don't poll exec response — the SSE stream is the only correct
    way to read output.

User's request: $ARGS
