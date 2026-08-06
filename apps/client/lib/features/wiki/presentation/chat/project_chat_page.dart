/// /wiki/p/:pid/chat —— 项目内对话（独立通道）。
///
/// 与 biumind 顶层 `/chat` 完全独立的会话体系：
///   - 后端：brain wiki/chat/api.go（B0.5 stub，10 端点）；表 schema
///     等 B5.x 加 migration。worker 上线后接通流式回复。
///   - 模型：knowcode 风格 conversations + messages CRUD。
///
/// B5.2 简化版：左侧 conversation 列表 + 右侧 message 流 + 底部输入。
/// 非流式（POST → 等响应）。等 brain chat worker 上线后升级为 SSE/WS。
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/form_factor.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/api/wiki_client.dart'
    show WikiChatMessage, WikiConversation;
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import 'chat_widgets.dart';

final _conversationsProvider =
    FutureProvider.family<List<WikiConversation>, String>(
  (ref, projectId) async {
    final repo = ref.watch(wikiRepositoryProvider);
    if (repo == null || projectId.isEmpty) return const [];
    return repo.client.listConversations(projectId);
  },
);

class ProjectChatPage extends ConsumerStatefulWidget {
  const ProjectChatPage({super.key, required this.projectId});
  final String projectId;

  @override
  ConsumerState<ProjectChatPage> createState() => _ProjectChatPageState();
}

class _ProjectChatPageState extends ConsumerState<ProjectChatPage> {
  String? _activeConversationId;
  List<WikiChatMessage> _messages = const [];
  bool _loadingMessages = false;
  bool _sending = false;
  String? _messagesError;

  final TextEditingController _inputCtrl = TextEditingController();
  final FocusNode _inputFocus = FocusNode();
  final ScrollController _scrollCtrl = ScrollController();

  @override
  void dispose() {
    _inputCtrl.dispose();
    _inputFocus.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  Future<void> _loadMessages(String convId) async {
    setState(() {
      _activeConversationId = convId;
      _messages = const [];
      _messagesError = null;
      _loadingMessages = true;
    });
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      setState(() {
        _loadingMessages = false;
        _messagesError = '未配置后端凭证';
      });
      return;
    }
    try {
      final msgs = await repo.client.listMessages(widget.projectId, convId);
      if (!mounted || _activeConversationId != convId) return;
      setState(() {
        _messages = msgs;
        _loadingMessages = false;
      });
      _scrollToBottom();
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _loadingMessages = false;
        _messagesError = '加载消息失败：$e';
      });
    }
  }

  Future<void> _newConversation() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    try {
      final conv = await repo.client.createConversation(widget.projectId);
      ref.invalidate(_conversationsProvider(widget.projectId));
      if (!mounted) return;
      setState(() {
        _activeConversationId = conv.id;
        _messages = const [];
      });
      _inputFocus.requestFocus();
    } on Exception catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('创建会话失败：$e')),
      );
    }
  }

  Future<void> _send() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty || _activeConversationId == null) return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    _inputCtrl.clear();
    final convId = _activeConversationId!;
    // 乐观追加用户消息（id 占位 'pending'）
    final pending = WikiChatMessage(
      id: 'pending-${DateTime.now().microsecondsSinceEpoch}',
      conversationId: convId,
      role: 'user',
      content: text,
      createdAt: DateTime.now().toUtc(),
    );
    setState(() {
      _messages = [..._messages, pending];
      _sending = true;
    });
    _scrollToBottom();
    try {
      await repo.client.sendMessage(
        widget.projectId,
        convId,
        content: text,
      );
      // 重新拉一次消息列表（拿 server 真实 id + 可能的 assistant 回复）
      final msgs = await repo.client.listMessages(widget.projectId, convId);
      if (!mounted || _activeConversationId != convId) return;
      setState(() {
        _messages = msgs;
        _sending = false;
      });
      _scrollToBottom();
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        // 移除乐观追加，因为发送失败
        _messages = _messages.where((m) => m.id != pending.id).toList();
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('发送失败：$e')),
      );
    }
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollCtrl.hasClients) return;
      _scrollCtrl.jumpTo(_scrollCtrl.position.maxScrollExtent);
    });
  }

  /// 让 brain 写一个 regenerate event，worker 接管 LLM 重新生成。
  /// 前端只 toast 即可（assistant 新消息会通过 syncws 推送到 list）。
  Future<void> _regenerate(WikiChatMessage msg) async {
    final convId = _activeConversationId;
    if (convId == null) return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    try {
      await repo.client
          .regenerateMessage(widget.projectId, convId, msg.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('已请求重新生成（worker 处理中）'),
          duration: Duration(seconds: 1),
        ),
      );
    } on Exception catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('重生成失败：$e')),
      );
    }
  }

  Future<void> _deleteMessage(WikiChatMessage msg) async {
    final convId = _activeConversationId;
    if (convId == null) return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除消息'),
        content: const Text('删除后无法恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton.tonal(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.errorContainer,
              foregroundColor: Theme.of(ctx).colorScheme.onErrorContainer,
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    // 乐观：先从本地 list 摘掉，brain 失败再回滚。
    final prev = _messages;
    setState(() {
      _messages = _messages.where((m) => m.id != msg.id).toList();
    });
    try {
      await repo.client.deleteMessage(widget.projectId, convId, msg.id);
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() => _messages = prev);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('删除失败：$e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final convs = ref.watch(_conversationsProvider(widget.projectId));
    final phone = isPhoneLayout(context);

    final Widget chatBody = _activeConversationId == null
        ? _Placeholder(onNew: _newConversation)
        : _ChatBody(
            loading: _loadingMessages,
            error: _messagesError,
            messages: _messages,
            sending: _sending,
            inputController: _inputCtrl,
            inputFocus: _inputFocus,
            scrollController: _scrollCtrl,
            onSend: _send,
            projectId: widget.projectId,
            onRegenerate: _regenerate,
            onDelete: _deleteMessage,
          );

    // 手机：240 左栏的会话列表收进 bottom sheet（顶栏「会话」按钮触发），
    // 聊天体占满。写法照 wiki_page._openPageListSheet 范式（§R1）。
    if (phone) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _SidebarHeader(
            onNew: _newConversation,
            onOpenConvs: () => _openConvSheet(context),
          ),
          Divider(height: 1, color: BiuTokens.borderSubtle),
          Expanded(child: chatBody),
        ],
      );
    }

    // 桌面：240 左栏 + 聊天体双栏（N0 原貌）。
    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SizedBox(
          width: 240,
          child: Container(
            color: BiuTokens.surfaceMuted,
            child: Column(
              children: [
                _SidebarHeader(onNew: _newConversation),
                Divider(height: 1, color: BiuTokens.borderSubtle),
                Expanded(child: _convList(convs, context)),
              ],
            ),
          ),
        ),
        Container(width: 1, color: BiuTokens.borderSubtle),
        Expanded(child: chatBody),
      ],
    );
  }

  /// 会话列表（桌面左栏 / 手机 bottom sheet 共用）。
  /// [inSheet] true 时选中会话先 pop 关 sheet 再加载消息。
  Widget _convList(
    AsyncValue<List<WikiConversation>> convs,
    BuildContext ctx, {
    bool inSheet = false,
  }) {
    return convs.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => _ErrorView(message: e.toString()),
      data: (list) {
        if (list.isEmpty) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(20),
              child: Text(
                '点击 + 新建对话',
                textAlign: TextAlign.center,
                style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
              ),
            ),
          );
        }
        return ListView.builder(
          padding: const EdgeInsets.symmetric(vertical: 4),
          itemCount: list.length,
          itemBuilder: (_, i) => _ConversationRow(
            conv: list[i],
            active: _activeConversationId == list[i].id,
            onTap: () {
              if (inSheet) Navigator.of(ctx).pop();
              _loadMessages(list[i].id);
            },
          ),
        );
      },
    );
  }

  /// 手机形态：会话列表（桌面 240 左栏）收进 bottom sheet 按需查看。
  /// 选中会话后 sheet 自动关闭。Consumer 实时 watch —— sheet 打开期间
  /// 新建会话能立即反映（不捕获 build 时的快照）。
  void _openConvSheet(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (sheetCtx) => Consumer(
        builder: (ctx, ref, _) {
          final convs = ref.watch(_conversationsProvider(widget.projectId));
          return SizedBox(
            height: MediaQuery.sizeOf(ctx).height * 0.7,
            child: Container(
              color: BiuTokens.surfaceMuted,
              child: Column(
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 8, 4),
                    child: Row(
                      children: [
                        Text(
                          '会话',
                          style: TextStyle(
                            color: BiuTokens.text,
                            fontSize: 14,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        const Spacer(),
                        IconButton(
                          tooltip: '新建会话',
                          onPressed: _newConversation,
                          icon: const Icon(Icons.add, size: 18),
                        ),
                      ],
                    ),
                  ),
                  Divider(height: 1, color: BiuTokens.borderSubtle),
                  Expanded(child: _convList(convs, sheetCtx, inSheet: true)),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

class _SidebarHeader extends StatelessWidget {
  const _SidebarHeader({required this.onNew, this.onOpenConvs});
  final VoidCallback onNew;
  /// 手机形态：打开会话列表 bottom sheet。null（桌面左栏头）不渲染按钮。
  final VoidCallback? onOpenConvs;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 44,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: Row(
        children: [
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          Icon(Icons.chat_bubble_outline,
              size: 14, color: BiuTokens.textSecondary),
          const SizedBox(width: 6),
          Text(
            '对话',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const Spacer(),
          // 手机形态：打开会话列表 bottom sheet（R2 单栏化）。
          if (onOpenConvs != null)
            IconButton(
              tooltip: '会话列表',
              onPressed: onOpenConvs,
              icon: const Icon(Icons.format_list_bulleted, size: 16),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            ),
          IconButton(
            tooltip: '新建会话',
            onPressed: onNew,
            icon: const Icon(Icons.add, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          ),
        ],
      ),
    );
  }
}

class _ConversationRow extends StatelessWidget {
  const _ConversationRow({
    required this.conv,
    required this.active,
    required this.onTap,
  });
  final WikiConversation conv;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final bg = active
        ? SemanticTokens.successSoft
        : Colors.transparent;
    return InkWell(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              conv.title.isEmpty ? '(未命名对话)' : conv.title,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                fontWeight: active ? FontWeight.w600 : FontWeight.w400,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            if (conv.messageCount > 0) ...[
              const SizedBox(height: 2),
              Text(
                '${conv.messageCount} 条消息',
                style: TextStyle(
                  color: BiuTokens.textMuted,
                  fontSize: 11,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _Placeholder extends StatelessWidget {
  const _Placeholder({required this.onNew});
  final VoidCallback onNew;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.chat_bubble_outline,
              size: 48, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Text(
            '选择一个会话，或新建一个开始',
            style: TextStyle(color: BiuTokens.text, fontSize: 14),
          ),
          const SizedBox(height: 12),
          FilledButton.icon(
            onPressed: onNew,
            icon: const Icon(Icons.add, size: 14),
            label: const Text('新建对话'),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.primary,
            ),
          ),
        ],
      ),
    );
  }
}

class _ChatBody extends StatelessWidget {
  const _ChatBody({
    required this.loading,
    required this.error,
    required this.messages,
    required this.sending,
    required this.inputController,
    required this.inputFocus,
    required this.scrollController,
    required this.onSend,
    required this.projectId,
    required this.onRegenerate,
    required this.onDelete,
  });

  final bool loading;
  final String? error;
  final List<WikiChatMessage> messages;
  final bool sending;
  final TextEditingController inputController;
  final FocusNode inputFocus;
  final ScrollController scrollController;
  final VoidCallback onSend;
  final String projectId;
  final Future<void> Function(WikiChatMessage) onRegenerate;
  final Future<void> Function(WikiChatMessage) onDelete;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(child: _buildBody()),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        _Composer(
          controller: inputController,
          focusNode: inputFocus,
          sending: sending,
          onSubmit: onSend,
        ),
      ],
    );
  }

  Widget _buildBody() {
    if (loading) return const Center(child: CircularProgressIndicator());
    if (error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: SelectableText(
            error!,
            style: TextStyle(color: BiuTokens.error, fontSize: 12),
          ),
        ),
      );
    }
    if (messages.isEmpty) {
      return Center(
        child: Text(
          '开始你的第一句话',
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
        ),
      );
    }
    return ListView.builder(
      controller: scrollController,
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
      itemCount: messages.length,
      itemBuilder: (_, i) => _MessageBubble(
        message: messages[i],
        projectId: projectId,
        onRegenerate: onRegenerate,
        onDelete: onDelete,
      ),
    );
  }
}

class _MessageBubble extends StatelessWidget {
  const _MessageBubble({
    required this.message,
    required this.projectId,
    required this.onRegenerate,
    required this.onDelete,
  });
  final WikiChatMessage message;
  final String projectId;
  final Future<void> Function(WikiChatMessage) onRegenerate;
  final Future<void> Function(WikiChatMessage) onDelete;

  @override
  Widget build(BuildContext context) {
    final isUser = message.role == 'user';
    final isAssistant = message.role == 'assistant';
    final split = isAssistant
        ? splitThinking(message.content)
        : ThinkingSplit(thinking: '', answer: message.content, isClosed: true);
    final cited = isAssistant
        ? citedPagesFromMetadata(message.metadata)
        : const <CitedPage>[];
    // 流式判定：pending id 通常没用（id 是 UUID），简化为：assistant 消息
    // 内容含 `<thinking>` 但未闭合 → streaming。
    final streaming = isAssistant && !split.isClosed;

    final bubble = Container(
      constraints: const BoxConstraints(maxWidth: 640),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: isUser ? BiuTokens.purpleLight : BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (split.thinking.isNotEmpty)
            ThinkingBlock(text: split.thinking, streaming: streaming),
          if (split.answer.isNotEmpty)
            SelectableText(
              split.answer,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
                height: 1.6,
              ),
            ),
          if (cited.isNotEmpty)
            CitedReferences(pages: cited, projectId: projectId),
        ],
      ),
    );

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment:
            isUser ? MainAxisAlignment.end : MainAxisAlignment.start,
        children: [
          if (!isUser) _AvatarDot(role: message.role),
          if (!isUser) const SizedBox(width: 8),
          Flexible(
            child: MessageActionsWrapper(
              content: message.content,
              onRegenerate: isAssistant ? () => onRegenerate(message) : null,
              onDelete: () => onDelete(message),
              child: bubble,
            ),
          ),
          if (isUser) const SizedBox(width: 8),
          if (isUser) _AvatarDot(role: 'user'),
        ],
      ),
    );
  }
}

class _AvatarDot extends StatelessWidget {
  const _AvatarDot({required this.role});
  final String role;

  @override
  Widget build(BuildContext context) {
    // user = brand (用户身份),assistant = accent (AI 身份),system = neutral。
    // 跟随当前色板,wiki 不再硬绑 emerald-green 作为 assistant 标识。
    final cs = Theme.of(context).colorScheme;
    final (icon, color) = switch (role) {
      'user' => (Icons.person_outline, cs.primary),
      'assistant' => (Icons.auto_awesome, cs.secondary),
      _ => (Icons.tune, BiuTokens.textMuted),
    };
    return Container(
      width: 26,
      height: 26,
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(13),
      ),
      alignment: Alignment.center,
      child: Icon(icon, size: 14, color: color),
    );
  }
}

class _Composer extends StatelessWidget {
  const _Composer({
    required this.controller,
    required this.focusNode,
    required this.sending,
    required this.onSubmit,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final bool sending;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: CallbackShortcuts(
              bindings: <ShortcutActivator, VoidCallback>{
                const SingleActivator(LogicalKeyboardKey.enter, meta: true):
                    onSubmit,
                const SingleActivator(LogicalKeyboardKey.enter, control: true):
                    onSubmit,
              },
              child: TextField(
                controller: controller,
                focusNode: focusNode,
                minLines: 1,
                maxLines: 6,
                decoration: InputDecoration(
                  hintText: '输入你的问题，⌘/Ctrl + Enter 发送',
                  hintStyle: TextStyle(
                    color: BiuTokens.textMuted,
                    fontSize: 12,
                  ),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
                    borderSide: BorderSide(color: BiuTokens.borderSubtle),
                  ),
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 10,
                  ),
                  isDense: true,
                ),
                style: TextStyle(color: BiuTokens.text, fontSize: 13),
              ),
            ),
          ),
          const SizedBox(width: 8),
          FilledButton.icon(
            onPressed: sending ? null : onSubmit,
            icon: sending
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : const Icon(Icons.send, size: 14),
            label: Text(sending ? '发送中…' : '发送'),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.primary,
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
              textStyle: const TextStyle(fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: SelectableText(
          message,
          style: TextStyle(color: BiuTokens.error, fontSize: 12),
        ),
      ),
    );
  }
}
