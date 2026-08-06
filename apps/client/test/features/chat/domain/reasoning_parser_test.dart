// ReasoningParser —— `<think>` 标签解析单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/reasoning_parser.dart';

void main() {
  group('parseReasoning', () {
    test('empty input returns empty list', () {
      expect(parseReasoning(''), isEmpty);
    });

    test('plain text without tags → single text segment', () {
      final segs = parseReasoning('hello world');
      expect(segs, hasLength(1));
      expect(segs.first.isText, isTrue);
      expect(segs.first.text, 'hello world');
      expect(segs.first.closed, isTrue);
    });

    test('closed `<think>` → single reasoning segment, closed=true', () {
      final segs = parseReasoning('<think>step 1\nstep 2</think>');
      expect(segs, hasLength(1));
      expect(segs.first.isReasoning, isTrue);
      expect(segs.first.text, 'step 1\nstep 2');
      expect(segs.first.closed, isTrue);
    });

    test('open `<think>` (streaming) → reasoning segment, closed=false', () {
      final segs = parseReasoning('<think>still thinking', isStreaming: true);
      expect(segs, hasLength(1));
      expect(segs.first.isReasoning, isTrue);
      expect(segs.first.text, 'still thinking');
      expect(segs.first.closed, isFalse);
    });

    test('reasoning followed by answer text → two segments', () {
      final segs = parseReasoning('<think>analysing</think>The answer is 42.');
      expect(segs, hasLength(2));
      expect(segs[0].isReasoning, isTrue);
      expect(segs[0].text, 'analysing');
      expect(segs[0].closed, isTrue);
      expect(segs[1].isText, isTrue);
      expect(segs[1].text, 'The answer is 42.');
    });

    test('text before `<think>` preserved as text segment', () {
      final segs = parseReasoning('Sure! <think>let me check</think>Done.');
      expect(segs, hasLength(3));
      expect(segs[0].isText, isTrue);
      expect(segs[0].text, 'Sure! ');
      expect(segs[1].isReasoning, isTrue);
      expect(segs[1].text, 'let me check');
      expect(segs[2].text, 'Done.');
    });

    test('multiple `<think>` blocks all captured', () {
      final segs = parseReasoning(
        '<think>a</think>between<think>b</think>after',
      );
      // think a / between / think b / after = 4 段
      expect(segs, hasLength(4));
      expect(segs[0].kind, ReasoningSegmentKind.reasoning);
      expect(segs[0].text, 'a');
      expect(segs[1].kind, ReasoningSegmentKind.text);
      expect(segs[1].text, 'between');
      expect(segs[2].kind, ReasoningSegmentKind.reasoning);
      expect(segs[2].text, 'b');
      expect(segs[3].kind, ReasoningSegmentKind.text);
      expect(segs[3].text, 'after');
    });

    test('streaming: closed reasoning + open second `<think>`', () {
      final segs = parseReasoning(
        '<think>first</think>partial answer <think>more reasoning',
        isStreaming: true,
      );
      expect(segs, hasLength(3));
      expect(segs[0].isReasoning, isTrue);
      expect(segs[0].closed, isTrue);
      expect(segs[1].isText, isTrue);
      expect(segs[1].text, 'partial answer ');
      expect(segs[2].isReasoning, isTrue);
      expect(segs[2].closed, isFalse);
      expect(segs[2].text, 'more reasoning');
    });

    test('empty `<think></think>` produces empty reasoning segment', () {
      final segs = parseReasoning('<think></think>hi');
      expect(segs, hasLength(2));
      expect(segs[0].isReasoning, isTrue);
      expect(segs[0].text, '');
      expect(segs[0].closed, isTrue);
      expect(segs[1].text, 'hi');
    });

    test('orphan `</think>` (no opener) treated as text', () {
      final segs = parseReasoning('plain </think> tail');
      expect(segs, hasLength(1));
      expect(segs.first.isText, isTrue);
      expect(segs.first.text, 'plain </think> tail');
    });

    test('case sensitivity: `<THINK>` is NOT recognised', () {
      // 推理模型业界标准用小写,大写当普通文本。
      final segs = parseReasoning('<THINK>not reasoning</THINK>');
      expect(segs, hasLength(1));
      expect(segs.first.isText, isTrue);
    });

    test('nested `<think>` — inner tag treated as text inside reasoning', () {
      // 不支持嵌套:第一个 `</think>` 闭合外层。注意:`<think>` 内若再有
      // `<think>` 则一直找到第一个 `</think>` 为闭合点,内部 `<think>` 当
      // 作 reasoning 文本的一部分。
      final segs = parseReasoning('<think>outer <think>inner</think>tail');
      // outer reasoning 段闭合于第一个 `</think>` → 'outer <think>inner'
      // 之后 'tail' 是 text。
      expect(segs, hasLength(2));
      expect(segs[0].isReasoning, isTrue);
      expect(segs[0].text, 'outer <think>inner');
      expect(segs[0].closed, isTrue);
      expect(segs[1].text, 'tail');
    });

    test('boundary: text ending with partial open tag stays as text', () {
      // 流式过程中可能先收到 `<thi` 这种半截标签 —— 当 text 不当 reasoning。
      final segs = parseReasoning('hello <thi', isStreaming: true);
      expect(segs, hasLength(1));
      expect(segs.first.isText, isTrue);
      expect(segs.first.text, 'hello <thi');
    });

    test('whitespace inside `<think>` preserved verbatim', () {
      final segs = parseReasoning('<think>\n  step 1\n  step 2\n</think>');
      expect(segs, hasLength(1));
      expect(segs.first.text, '\n  step 1\n  step 2\n');
    });
  });

  group('hasReasoningTag', () {
    test('returns true when `<think>` present', () {
      expect(hasReasoningTag('hello <think>x</think>'), isTrue);
      expect(hasReasoningTag('<think>open'), isTrue);
    });

    test('returns false on plain text or partial tag', () {
      expect(hasReasoningTag('hello world'), isFalse);
      expect(hasReasoningTag('<thi'), isFalse);
      expect(hasReasoningTag('<THINK>'), isFalse);
    });
  });
}
