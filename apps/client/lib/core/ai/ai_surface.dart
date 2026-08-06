// AiSurface — unified AI invocation across features.
//
// Per Client Architecture invariant C9: any AI call must go through this
// surface. Features never import LLM clients directly.
//
// Pluggable [AiSurfaceBackend] lets us route to:
//   - RelayBackend (Mode A/B): server-routed; default
//   - DirectBackend (Mode C): user's own provider — landed in P3.5

import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Coarse classification — chooses prompt template + default model + budget.
enum AiSurfaceKind {
  chat,
  wikiInline,
  wikiDeepResearch,
  code,
  translate,
  voice,
  custom,
}

class AiPolicy {
  final int? maxCostMicroUsd;
  final List<String>? allowTools;
  final String? preferModel;
  const AiPolicy({this.maxCostMicroUsd, this.allowTools, this.preferModel});
}

class AiInvocation {
  final String intent;             // "summarize" / "rewrite" / "chat" / ...
  final Object input;              // String / List<Message> / Block etc
  final Map<String, Object?> context;
  final AiSurfaceKind surface;
  final AiPolicy? policy;

  const AiInvocation({
    required this.intent,
    required this.input,
    required this.surface,
    this.context = const {},
    this.policy,
  });
}

/// One streaming chunk delivered to AiSurface callers.
class AiChunk {
  final AiChunkKind kind;
  final String? text;
  final String? toolCallId;
  final String? toolName;
  final String? error;
  const AiChunk._({required this.kind, this.text, this.toolCallId, this.toolName, this.error});
  factory AiChunk.text(String t) => AiChunk._(kind: AiChunkKind.text, text: t);
  factory AiChunk.toolStart(String id, String name) =>
      AiChunk._(kind: AiChunkKind.toolCallStart, toolCallId: id, toolName: name);
  factory AiChunk.toolArgs(String id, String delta) =>
      AiChunk._(kind: AiChunkKind.toolCallArgs, toolCallId: id, text: delta);
  factory AiChunk.toolEnd(String id) =>
      AiChunk._(kind: AiChunkKind.toolCallEnd, toolCallId: id);
  factory AiChunk.error(String msg) => AiChunk._(kind: AiChunkKind.error, error: msg);
  factory AiChunk.done() => const AiChunk._(kind: AiChunkKind.done);
}

enum AiChunkKind { text, toolCallStart, toolCallArgs, toolCallEnd, error, done }

class AiResult {
  final String text;
  final int? costMicroUsd;
  final int? tokensIn;
  final int? tokensOut;
  const AiResult({required this.text, this.costMicroUsd, this.tokensIn, this.tokensOut});
}

/// Pluggable backend; concrete impls live in `data/ai/*` and are wired by
/// [aiSurfaceProvider] depending on user mode (cloud / direct).
abstract class AiSurfaceBackend {
  Stream<AiChunk> invoke(AiInvocation inv);
  Future<AiResult> invokeBlocking(AiInvocation inv) async {
    final buf = StringBuffer();
    String? err;
    await for (final c in invoke(inv)) {
      switch (c.kind) {
        case AiChunkKind.text:
          buf.write(c.text);
        case AiChunkKind.error:
          err = c.error;
        case _:
          break;
      }
    }
    if (err != null) {
      throw AiSurfaceError(err);
    }
    return AiResult(text: buf.toString());
  }
}

class AiSurfaceError implements Exception {
  final String message;
  const AiSurfaceError(this.message);
  @override
  String toString() => 'AiSurfaceError: $message';
}

/// Default backend used in tests / dev-mode without server: emits a stub
/// reply. Real apps register RelayBackend / DirectBackend over this provider.
class DevEchoBackend extends AiSurfaceBackend {
  @override
  Stream<AiChunk> invoke(AiInvocation inv) async* {
    yield AiChunk.text('[dev-echo:${inv.surface.name}] ');
    yield AiChunk.text(inv.input.toString());
    yield AiChunk.done();
  }
}

class AiSurface {
  AiSurface(this._backend);
  AiSurfaceBackend _backend;

  /// Replace the backend at runtime (used when user switches mode).
  void setBackend(AiSurfaceBackend backend) {
    _backend = backend;
  }

  Stream<AiChunk> invoke(AiInvocation inv) => _backend.invoke(inv);

  Future<AiResult> invokeBlocking(AiInvocation inv) => _backend.invokeBlocking(inv);
}

/// Backend factory — overridden in main.dart to inject RelayBackend when creds
/// are present; default returns DevEchoBackend for tests / no-config dev runs.
final aiSurfaceBackendProvider = Provider<AiSurfaceBackend>((ref) {
  return DevEchoBackend();
});

/// Single live AiSurface; rebuilt when backend provider changes.
final aiSurfaceProvider = Provider<AiSurface>((ref) {
  return AiSurface(ref.watch(aiSurfaceBackendProvider));
});
