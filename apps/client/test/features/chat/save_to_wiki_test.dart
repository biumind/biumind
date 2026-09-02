// save_to_wiki_dialog 纯函数契约：think 块清洗 + 标题提取。
// 对齐 llm_wiki chat-save-to-wiki.ts 的行为（Apache-2.0 重写）。

import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/features/chat/presentation/v2/save_to_wiki_dialog.dart';

void main() {
  group('cleanAssistantContentForWikiSave', () {
    test('strips closed think block', () {
      const raw = '<think>reasoning here</think>正文内容';
      expect(cleanAssistantContentForWikiSave(raw), '正文内容');
    });

    test('strips thinking variant case-insensitively', () {
      const raw = '<Thinking>long\nreasoning</Thinking>\n答案';
      expect(cleanAssistantContentForWikiSave(raw), '答案');
    });

    test('strips unclosed streaming think tail', () {
      const raw = '<think>半截推理还没完';
      expect(cleanAssistantContentForWikiSave(raw), '');
    });

    test('strips save-worthy and sources comment markers', () {
      const raw = '<!-- save-worthy: true -->\n正文\n<!-- sources:1,2 -->';
      expect(cleanAssistantContentForWikiSave(raw), '正文');
    });

    test('keeps normal markdown untouched', () {
      const raw = '# 标题\n\n正文 **加粗** `代码`';
      expect(cleanAssistantContentForWikiSave(raw), raw);
    });
  });

  group('titleFromCleanAssistantContent', () {
    test('first visible line, heading marks stripped', () {
      expect(titleFromCleanAssistantContent('\n## 核心结论\n正文'), '核心结论');
    });

    test('truncates to 60 chars', () {
      final long = 'a' * 80;
      expect(titleFromCleanAssistantContent(long).length, 60);
    });

    test('empty falls back to placeholder', () {
      expect(titleFromCleanAssistantContent('  \n \n'), '未命名保存');
    });
  });
}
