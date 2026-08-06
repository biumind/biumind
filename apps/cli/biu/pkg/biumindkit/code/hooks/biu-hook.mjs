#!/usr/bin/env node
// BiuMind hook bridge — managed by the BiuMind app (biu serve).
// 仅在 BIU_TASK_ID + BIU_EVENT_DIR 同时存在时收集事件;其它场景(用户手动启动
// claude/codex)直接退出,零副作用。

import { appendFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const taskId = process.env.BIU_TASK_ID;
const eventDir = process.env.BIU_EVENT_DIR;
if (!taskId || !eventDir) {
  process.exit(0);
}

// 不同 agent 的 payload 字段名不一致:Claude 用 hook_event_name / session_id,
// Codex 用 event_name / conversation_id;再退到 agent 自带的环境变量。
const pick = (payload, ...keys) => {
  for (const k of keys) {
    const v = payload[k];
    if (typeof v === "string" && v) return v;
  }
  return "";
};

let raw = "";
let done = false;

// 用已收集到的 stdin 内容落盘并退出。幂等:end / error / uncaughtException
// 任一触发都只执行一次,且永远 exit 0 —— 绝不让 hook 失败影响 agent。
function finish() {
  if (done) return;
  done = true;
  try {
    const payload = raw ? JSON.parse(raw) : {};
    const line =
      JSON.stringify({
        ts: Date.now(),
        task_id: taskId,
        agent: process.env.BIU_AGENT || "",
        event: pick(payload, "hook_event_name", "event_name", "hookEventName", "event"),
        session_id:
          pick(payload, "session_id", "conversation_id", "sessionId", "conversationId") ||
          process.env.CODEX_SESSION_ID ||
          process.env.CLAUDE_CODE_SESSION_ID ||
          "",
        transcript_path: pick(payload, "transcript_path", "transcriptPath", "rollout_path"),
        cwd: pick(payload, "cwd"),
        tool_name: pick(payload, "tool_name", "toolName"),
        permission_mode: pick(payload, "permission_mode", "permissionMode"),
      }) + "\n";
    mkdirSync(eventDir, { recursive: true });
    appendFileSync(join(eventDir, "events.jsonl"), line);
  } catch {
    // 永远不要让 hook 失败导致 agent 阻塞
  }
  process.exit(0);
}

process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  raw += chunk;
});
process.stdin.on("end", finish);
// Windows 关键修复:agent 关闭 stdin 管道后 Node 读到 EOF 会抛 'error' 事件
// (Unix 下干净触发 'end')。无监听器会变未捕获异常令进程 exit 1,agent 报
// "hook exited with code 1"。此时 payload 已收齐,正常落盘即可。
process.stdin.on("error", finish);
// 兜底:任何未预期异常都不得令 hook 以非 0 退出。
process.on("uncaughtException", finish);
