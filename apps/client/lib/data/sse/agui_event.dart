// AG-UI event types per https://docs.ag-ui.com (validated against
// services/runtime/internal/agent/agent.go output).
//
// Sealed class hierarchy: each event has its own subclass with strongly-typed
// payload. Unknown / future event types are preserved as [UnknownEvent] so the
// stream is forward-compatible per the AG-UI extension contract.

import 'dart:convert';

sealed class AgUiEvent {
  const AgUiEvent();

  /// Parse one frame from the Realtime SSE stream.
  /// `wireType` is the value from `payload.type` (or top-level `type` for
  /// non-Realtime contexts).
  /// `payload` is the JSON payload object.
  static AgUiEvent parse(String wireType, Map<String, dynamic> payload) {
    switch (wireType) {
      // Lifecycle
      case 'RUN_STARTED':
        return RunStarted(
          threadId: payload['threadId'] as String? ?? '',
          runId: payload['runId'] as String? ?? '',
        );
      case 'RUN_FINISHED':
        return RunFinished(
          threadId: payload['threadId'] as String? ?? '',
          runId: payload['runId'] as String? ?? '',
          result: payload['result'],
        );
      case 'RUN_ERROR':
        return RunError(
          message: payload['message'] as String? ?? '',
          code: payload['code'] as String?,
        );
      case 'STEP_STARTED':
        return StepStarted(stepName: payload['stepName'] as String? ?? '');
      case 'STEP_FINISHED':
        return StepFinished(stepName: payload['stepName'] as String? ?? '');

      // Text
      case 'TEXT_MESSAGE_START':
        return TextMessageStart(
          messageId: payload['messageId'] as String? ?? '',
          role: payload['role'] as String? ?? 'assistant',
        );
      case 'TEXT_MESSAGE_CONTENT':
        return TextMessageContent(
          messageId: payload['messageId'] as String? ?? '',
          delta: payload['delta'] as String? ?? '',
        );
      case 'TEXT_MESSAGE_END':
        return TextMessageEnd(messageId: payload['messageId'] as String? ?? '');

      // Tools
      case 'TOOL_CALL_START':
        return ToolCallStart(
          toolCallId: payload['toolCallId'] as String? ?? '',
          toolCallName: payload['toolCallName'] as String? ?? '',
          parentMessageId: payload['parentMessageId'] as String?,
        );
      case 'TOOL_CALL_ARGS':
        return ToolCallArgs(
          toolCallId: payload['toolCallId'] as String? ?? '',
          delta: payload['delta'] as String? ?? '',
        );
      case 'TOOL_CALL_END':
        return ToolCallEnd(toolCallId: payload['toolCallId'] as String? ?? '');
      case 'TOOL_CALL_RESULT':
        return ToolCallResult(
          toolCallId: payload['toolCallId'] as String? ?? '',
          content: payload['content'] as String? ?? '',
          error: payload['error'] as String?,
        );

      // State
      case 'STATE_SNAPSHOT':
        return StateSnapshot(snapshot: payload['snapshot']);
      case 'STATE_DELTA':
        return StateDelta(delta: payload['delta']);
      case 'MESSAGES_SNAPSHOT':
        final raw = payload['messages'] as List? ?? const [];
        return MessagesSnapshot(messages: raw.cast<Map<String, dynamic>>());

      // BiuMind CUSTOM extensions (under one wrapper)
      case 'CUSTOM':
        final name = payload['name'] as String? ?? '';
        final value = (payload['value'] as Map?)?.cast<String, dynamic>() ?? const {};
        return CustomEvent.dispatch(name, value);

      case 'RAW':
        return RawPassthrough(raw: payload);
    }
    return UnknownEvent(type: wireType, payload: payload);
  }

  /// Convenience: parse a wire envelope `{ "type": "...", ...rest }`.
  static AgUiEvent parseEnvelope(Map<String, dynamic> envelope) {
    final type = envelope['type'] as String? ?? '';
    return parse(type, envelope);
  }
}

// ─── Lifecycle ─────────────────────────────────────────

class RunStarted extends AgUiEvent {
  final String threadId;
  final String runId;
  const RunStarted({required this.threadId, required this.runId});
}

class RunFinished extends AgUiEvent {
  final String threadId;
  final String runId;
  final Object? result;
  const RunFinished({required this.threadId, required this.runId, this.result});
}

class RunError extends AgUiEvent {
  final String message;
  final String? code;
  const RunError({required this.message, this.code});
}

class StepStarted extends AgUiEvent {
  final String stepName;
  const StepStarted({required this.stepName});
}

class StepFinished extends AgUiEvent {
  final String stepName;
  const StepFinished({required this.stepName});
}

// ─── Text ──────────────────────────────────────────────

class TextMessageStart extends AgUiEvent {
  final String messageId;
  final String role;
  const TextMessageStart({required this.messageId, required this.role});
}

class TextMessageContent extends AgUiEvent {
  final String messageId;
  final String delta;
  const TextMessageContent({required this.messageId, required this.delta});
}

class TextMessageEnd extends AgUiEvent {
  final String messageId;
  const TextMessageEnd({required this.messageId});
}

// ─── Tools ─────────────────────────────────────────────

class ToolCallStart extends AgUiEvent {
  final String toolCallId;
  final String toolCallName;
  final String? parentMessageId;
  const ToolCallStart({
    required this.toolCallId,
    required this.toolCallName,
    this.parentMessageId,
  });
}

class ToolCallArgs extends AgUiEvent {
  final String toolCallId;
  final String delta;
  const ToolCallArgs({required this.toolCallId, required this.delta});

  /// Parse the accumulated args JSON. Caller responsibility to accumulate
  /// `delta` strings across multiple events for the same toolCallId.
  static Map<String, dynamic>? parseArgs(String accumulated) {
    if (accumulated.isEmpty) return null;
    try {
      return jsonDecode(accumulated) as Map<String, dynamic>;
    } catch (_) {
      return null;
    }
  }
}

class ToolCallEnd extends AgUiEvent {
  final String toolCallId;
  const ToolCallEnd({required this.toolCallId});
}

class ToolCallResult extends AgUiEvent {
  final String toolCallId;
  final String content;
  final String? error;
  const ToolCallResult({required this.toolCallId, required this.content, this.error});
}

// ─── State ─────────────────────────────────────────────

class StateSnapshot extends AgUiEvent {
  final Object? snapshot;
  const StateSnapshot({this.snapshot});
}

class StateDelta extends AgUiEvent {
  final Object? delta; // RFC 6902 JSON Patch
  const StateDelta({this.delta});
}

class MessagesSnapshot extends AgUiEvent {
  final List<Map<String, dynamic>> messages;
  const MessagesSnapshot({required this.messages});
}

// ─── Special ───────────────────────────────────────────

class RawPassthrough extends AgUiEvent {
  final Map<String, dynamic> raw;
  const RawPassthrough({required this.raw});
}

class UnknownEvent extends AgUiEvent {
  final String type;
  final Map<String, dynamic> payload;
  const UnknownEvent({required this.type, required this.payload});
}

// ─── BiuMind CUSTOM events (registered names: biumind.<domain>.<event>) ───

sealed class CustomEvent extends AgUiEvent {
  const CustomEvent();

  static AgUiEvent dispatch(String name, Map<String, dynamic> value) {
    switch (name) {
      case 'biumind.permission.requested':
        return PermissionRequested(
          callId: value['call_id'] as String? ?? '',
          tool: value['tool'] as String? ?? '',
          args: (value['args'] as Map?)?.cast<String, dynamic>() ?? const {},
          suggestedPolicy: value['suggested_policy'] as String?,
        );
      case 'biumind.cost.update':
        return CostUpdate(
          tokensIn: (value['tokens_in'] as num? ?? 0).toInt(),
          tokensOut: (value['tokens_out'] as num? ?? 0).toInt(),
          costMicroUsd: (value['cost_micro_usd'] as num? ?? 0).toInt(),
          model: value['model'] as String? ?? '',
        );
      case 'biumind.budget.exceeded':
        return BudgetExceeded(
          limitMicroUsd: (value['limit'] as num? ?? 0).toInt(),
          spentMicroUsd: (value['spent'] as num? ?? 0).toInt(),
          scope: value['scope'] as String? ?? '',
        );
      case 'biumind.heartbeat':
        return const HeartbeatCustom();
      case 'biumind.task.linked':
        return TaskLinked(
          taskId: value['task_id'] as String? ?? '',
          dependsOn: ((value['depends_on'] as List?) ?? const []).cast<String>(),
        );
    }
    return UnregisteredCustomEvent(name: name, value: value);
  }
}

class PermissionRequested extends CustomEvent {
  final String callId;
  final String tool;
  final Map<String, dynamic> args;
  final String? suggestedPolicy;
  const PermissionRequested({
    required this.callId,
    required this.tool,
    required this.args,
    this.suggestedPolicy,
  });
}

class CostUpdate extends CustomEvent {
  final int tokensIn;
  final int tokensOut;
  final int costMicroUsd;
  final String model;
  const CostUpdate({
    required this.tokensIn,
    required this.tokensOut,
    required this.costMicroUsd,
    required this.model,
  });
}

class BudgetExceeded extends CustomEvent {
  final int limitMicroUsd;
  final int spentMicroUsd;
  final String scope;
  const BudgetExceeded({
    required this.limitMicroUsd,
    required this.spentMicroUsd,
    required this.scope,
  });
}

class HeartbeatCustom extends CustomEvent {
  const HeartbeatCustom();
}

class TaskLinked extends CustomEvent {
  final String taskId;
  final List<String> dependsOn;
  const TaskLinked({required this.taskId, required this.dependsOn});
}

class UnregisteredCustomEvent extends CustomEvent {
  final String name;
  final Map<String, dynamic> value;
  const UnregisteredCustomEvent({required this.name, required this.value});
}
