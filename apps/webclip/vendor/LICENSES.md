# Vendored libraries

These files are unmodified copies of upstream open-source libraries.
BiuMind doesn't author or modify them; we ship them inside the extension
because Chrome MV3 has no module bundler and we need them present in
the tab's MAIN world to run page extraction client-side.

## readability.js

- Upstream: https://github.com/mozilla/readability
- License:  Apache-2.0 (Copyright (c) 2010 Arc90 Inc)
- Header preserved at the top of the file.

## turndown.js

- Upstream: https://github.com/mixmark-io/turndown
- License:  MIT (Copyright (c) 2017+ Dom Christie)
- The UMD bundle ships without an inline header; the canonical license
  text lives at the upstream repo root. We track the version we
  imported in the parent README's "Vendor" section.
