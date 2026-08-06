import 'package:biumind/core/ai/ai_surface.dart';
import 'package:flutter_test/flutter_test.dart';

class _ScriptedBackend extends AiSurfaceBackend {
  _ScriptedBackend(this.script);
  final List<AiChunk> script;
  AiInvocation? lastInvocation;

  @override
  Stream<AiChunk> invoke(AiInvocation inv) async* {
    lastInvocation = inv;
    for (final c in script) {
      yield c;
    }
  }
}

void main() {
  test('streams text chunks', () async {
    final backend = _ScriptedBackend([
      AiChunk.text('Hello, '),
      AiChunk.text('world'),
      AiChunk.done(),
    ]);
    final surface = AiSurface(backend);
    final result = await surface.invokeBlocking(const AiInvocation(
      intent: 'chat',
      input: 'hi',
      surface: AiSurfaceKind.chat,
    ));
    expect(result.text, 'Hello, world');
  });

  test('forwards invocation context', () async {
    final backend = _ScriptedBackend([AiChunk.done()]);
    final surface = AiSurface(backend);
    await surface.invokeBlocking(const AiInvocation(
      intent: 'rewrite',
      input: 'x',
      surface: AiSurfaceKind.wikiInline,
      context: {'tone': 'formal'},
    ));
    expect(backend.lastInvocation?.intent, 'rewrite');
    expect(backend.lastInvocation?.surface, AiSurfaceKind.wikiInline);
    expect(backend.lastInvocation?.context['tone'], 'formal');
  });

  test('error frame surfaces as exception in invokeBlocking', () async {
    final backend = _ScriptedBackend([
      AiChunk.text('partial'),
      AiChunk.error('boom'),
    ]);
    final surface = AiSurface(backend);
    expect(
      () => surface.invokeBlocking(const AiInvocation(intent: 'x', input: 'y', surface: AiSurfaceKind.chat)),
      throwsA(isA<AiSurfaceError>()),
    );
  });

  test('setBackend swaps live', () async {
    final s = AiSurface(_ScriptedBackend([AiChunk.text('A'), AiChunk.done()]));
    final r1 = await s.invokeBlocking(const AiInvocation(intent: 'x', input: 'y', surface: AiSurfaceKind.chat));
    expect(r1.text, 'A');
    s.setBackend(_ScriptedBackend([AiChunk.text('B'), AiChunk.done()]));
    final r2 = await s.invokeBlocking(const AiInvocation(intent: 'x', input: 'y', surface: AiSurfaceKind.chat));
    expect(r2.text, 'B');
  });
}
