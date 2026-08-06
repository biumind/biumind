// Backward-compat re-export — see theme/theme.dart for the spec.
//
// 老代码 `import 'app/theme.dart';` 自动拿到新主题系统的所有公共 API
// (BiuTokens compat shim / BiuColors / BiuMetrics / buildTheme / ...).
//
// **新代码请直接 `import 'app/theme/theme.dart';`** — 这个文件只是过渡桥。

import 'package:flutter/material.dart';

import 'theme/font_size.dart';
import 'theme/palettes.dart';
import 'theme/theme_builder.dart';

export 'theme/theme.dart';

/// 老 `BiuTheme.light()` / `BiuTheme.dark()` 兼容入口。
/// 新代码用 `buildTheme(palette:..., mode:..., fontSize:...)` 直接传参。
@Deprecated('Use buildTheme(palette: ..., mode: ..., fontSize: ...) directly')
class BiuTheme {
  static ThemeData light() => buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.light,
        fontSize: FontSize.small,
      );

  static ThemeData dark() => buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.dark,
        fontSize: FontSize.small,
      );
}
