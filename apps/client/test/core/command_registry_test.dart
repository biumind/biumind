import 'package:biumind/core/commands/command_registry.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('register and invoke', () async {
    final reg = CommandRegistry();
    var fired = 0;
    reg.register(Command(
      id: 'test.fire',
      label: 'Fire',
      handler: (_) => fired++,
    ));
    final ok = await reg.invoke('test.fire', const CommandContext());
    expect(ok, isTrue);
    expect(fired, 1);
  });

  test('invoke unknown returns false', () async {
    final reg = CommandRegistry();
    final ok = await reg.invoke('nope', const CommandContext());
    expect(ok, isFalse);
  });

  test('when clause blocks invocation', () async {
    final reg = CommandRegistry();
    var fired = 0;
    reg.register(Command(
      id: 'test.gated',
      label: 'Gated',
      when: 'has.editor',
      handler: (_) => fired++,
    ));
    var available = false;
    reg.registerWhenEvaluator('has.editor', (_) => available);

    expect(await reg.invoke('test.gated', const CommandContext()), isFalse);
    expect(fired, 0);

    available = true;
    expect(await reg.invoke('test.gated', const CommandContext()), isTrue);
    expect(fired, 1);
  });

  test('aiInvokable subset', () {
    final reg = CommandRegistry();
    reg.register(Command(id: 'a', label: 'A', handler: (_) {}));
    reg.register(Command(id: 'b', label: 'B', aiInvokable: true, handler: (_) {}));
    final ai = reg.aiInvokable();
    expect(ai.length, 1);
    expect(ai.first.id, 'b');
  });

  test('byCategory filters', () {
    final reg = CommandRegistry();
    reg.register(Command(id: 'a', label: 'A', category: CommandCategory.create, handler: (_) {}));
    reg.register(Command(id: 'b', label: 'B', category: CommandCategory.edit, handler: (_) {}));
    final create = reg.byCategory(CommandCategory.create, const CommandContext());
    expect(create.map((c) => c.id), ['a']);
  });
}
