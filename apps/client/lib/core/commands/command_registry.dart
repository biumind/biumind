// CommandRegistry — central registry for every UI action.
//
// Per Client Architecture invariant C8: any UI action must go through this
// registry. Direct controller method calls from widgets are not allowed.
//
// MVP scope: register / lookup / invoke / list-by-category. Cmd+K palette,
// keyboard binding override, and AI invocation hook layer on top.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// CommandContext is passed to every handler.
class CommandContext {
  final WidgetRef? ref;            // null for non-UI invocations (AI / scripts)
  final BuildContext? buildContext; // null for non-UI invocations
  final Map<String, Object?> args;
  const CommandContext({this.ref, this.buildContext, this.args = const {}});
}

enum CommandCategory { create, edit, navigate, view, share, system, misc }

class Command {
  final String id;
  final String label;            // i18n key for display
  final String? description;
  final IconData? icon;
  final SingleActivator? defaultShortcut;
  final String? when;            // expression evaluated against ctx; null = always available
  final CommandCategory category;
  final bool aiInvokable;        // expose to AI / MCP tool layer
  final FutureOr<void> Function(CommandContext) handler;

  const Command({
    required this.id,
    required this.label,
    required this.handler,
    this.description,
    this.icon,
    this.defaultShortcut,
    this.when,
    this.category = CommandCategory.misc,
    this.aiInvokable = false,
  });
}

class CommandRegistry {
  final Map<String, Command> _commands = {};
  final Map<String, bool Function(CommandContext)> _whenEvaluators = {};

  /// Register a command. Replaces any existing command with the same id.
  void register(Command c) {
    _commands[c.id] = c;
  }

  void registerAll(Iterable<Command> commands) {
    for (final c in commands) {
      register(c);
    }
  }

  /// Register a custom evaluator for a `when` clause name.
  /// Used by features that publish context vars (e.g. wiki.activePage).
  void registerWhenEvaluator(String name, bool Function(CommandContext) eval) {
    _whenEvaluators[name] = eval;
  }

  Command? lookup(String id) => _commands[id];

  /// Returns commands whose `when` clause currently evaluates true.
  List<Command> available(CommandContext ctx) {
    return _commands.values.where((c) => _whenIsTrue(c.when, ctx)).toList();
  }

  List<Command> byCategory(CommandCategory cat, CommandContext ctx) {
    return available(ctx).where((c) => c.category == cat).toList();
  }

  /// AI / MCP exposable commands (subset).
  List<Command> aiInvokable() {
    return _commands.values.where((c) => c.aiInvokable).toList();
  }

  /// Invoke a command by id. Returns false if not found or `when` blocks it.
  Future<bool> invoke(String id, CommandContext ctx) async {
    final c = _commands[id];
    if (c == null) return false;
    if (!_whenIsTrue(c.when, ctx)) return false;
    await c.handler(ctx);
    return true;
  }

  bool _whenIsTrue(String? when, CommandContext ctx) {
    if (when == null || when.isEmpty) return true;
    final eval = _whenEvaluators[when];
    if (eval == null) return true; // unknown evaluator → don't block
    return eval(ctx);
  }
}

/// Process-global Riverpod provider.
final commandRegistryProvider = Provider<CommandRegistry>((ref) {
  return CommandRegistry();
});
