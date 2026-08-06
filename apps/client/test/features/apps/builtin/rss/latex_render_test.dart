// 端到端验证:块级公式经 injectDisplayTex + HtmlWidget.customWidgetBuilder
// 真的渲成 Math widget(而不是残留为文本 / <x-tex> 标签)。

import 'package:biumind/features/apps/builtin/rss/widgets/latex_html.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_widget_from_html_core/flutter_widget_from_html_core.dart';

void main() {
  testWidgets(r'块级 $$ 公式渲成 RssMathBlock + Math widget', (tester) async {
    const html = r'<p>看公式:</p>$$E = mc^2$$<p>结束。</p>';
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: HtmlWidget(
          injectDisplayTex(html),
          customWidgetBuilder: (element) {
            if (element.localName == kTexTag) {
              return RssMathBlock(latex: decodeTex(element.text));
            }
            return null;
          },
        ),
      ),
    ));
    await tester.pumpAndSettle();

    expect(find.byType(RssMathBlock), findsOneWidget);
    expect(find.byType(Math), findsOneWidget);
  });
}
