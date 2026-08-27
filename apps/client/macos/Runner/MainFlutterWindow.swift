import Cocoa
import FlutterMacOS
import WebKit

class MainFlutterWindow: NSWindow {
  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)

    RegisterGeneratedPlugins(registry: flutterViewController)

    // 红绿灯内嵌（WorkBuddy 风）: 内容延伸进标题栏区域, 标题栏透明 +
    // 隐藏标题文字。Flutter 侧顶部绘 40px 窗口条 (左侧为红绿灯留位),
    // 拖拽/双击缩放走下面的 biumind/window channel。
    styleMask.insert(.fullSizeContentView)
    titlebarAppearsTransparent = true
    titleVisibility = .hidden

    // 最小窗口尺寸 — 固定表单页(登录/设置等)按 1024×640 设计,「滚动条可见
    // ⟺ 内容真的超长」不变量依赖这个下限(见 core/ui/biu_scroll_behavior.dart)。
    // storyboard 默认 800×600 低于下限, 首启时钳制到最小尺寸。
    minSize = NSSize(width: 1024, height: 640)
    if frame.width < minSize.width || frame.height < minSize.height {
      var f = frame
      f.size.width = max(f.size.width, minSize.width)
      f.size.height = max(f.size.height, minSize.height)
      setFrame(f, display: true)
    }

    // 平台视图点击的焦点接管：落在 WKWebView（笔记编辑器 / apps 面板）
    // 上的点击进不了 Flutter 手势体系 —— 文本框 FocusNode 不会自动
    // unfocus，FlutterTextInputPlugin 的隐藏输入框握着第一响应者不放
    // （编辑会话未结束拒绝 resign），表现为点击编辑器后无法编辑。
    // 这里：通知 Dart 侧 unfocus 收敛框架焦点，并在点击落定后（若原生
    // 层未能自行移交）结束编辑会话、强制移交第一响应者。
    let focusChannel = FlutterMethodChannel(
      name: "biumind/focus",
      binaryMessenger: flutterViewController.engine.binaryMessenger)
    NSEvent.addLocalMonitorForEvents(matching: .leftMouseDown) { [weak self] ev in
      guard let w = self, ev.window === w,
            let wv = w.webView(at: ev.locationInWindow) else { return ev }
      focusChannel.invokeMethod("platformViewTapped", arguments: nil)
      DispatchQueue.main.async {
        if w.firstResponder !== wv {
          w.endEditing(for: nil)
          w.makeFirstResponder(wv)
        }
      }
      return ev
    }

    let windowChannel = FlutterMethodChannel(
      name: "biumind/window",
      binaryMessenger: flutterViewController.engine.binaryMessenger)
    windowChannel.setMethodCallHandler { [weak self] call, result in
      guard let self = self else {
        result(nil)
        return
      }
      switch call.method {
      case "drag":
        // 由 Flutter WindowDragArea 的 pan 手势触发; currentEvent 是
        // mouseDown/mouseDragged, performDrag 接管后续拖动直到松开。
        if let event = NSApp.currentEvent {
          self.performDrag(with: event)
        }
        result(nil)
      case "zoom":
        self.zoom(nil)
        result(nil)
      case "trafficLights":
        // 红绿灯在窗口坐标(左上原点, logical pt)中的几何:
        //   centerY = 按钮垂直中心距窗口顶; right = 绿灯右缘。
        // Flutter 侧顶栏据此对齐 toggle 与红绿灯同一行, 不猜固定值。
        guard let close = self.standardWindowButton(.closeButton),
              let zoom = self.standardWindowButton(.zoomButton) else {
          result(nil)
          return
        }
        let closeF = close.convert(close.bounds, to: nil)
        let zoomF = zoom.convert(zoom.bounds, to: nil)
        result([
          "centerY": self.frame.height - closeF.midY,
          "right": zoomF.maxX,
        ])
      default:
        result(FlutterMethodNotImplemented)
      }
    }

    // 双格式剪贴板（编辑器右键菜单「复制」P2）：Flutter Clipboard 只支持
    // 纯文本，text+html 双格式走这里写 NSPasteboard —— 粘到 Word/飞书/
    // 邮件等外部应用保留格式（表格、代码块等）。
    let clipboardChannel = FlutterMethodChannel(
      name: "biumind/clipboard",
      binaryMessenger: flutterViewController.engine.binaryMessenger)
    clipboardChannel.setMethodCallHandler { call, result in
      switch call.method {
      case "writeRich":
        guard let args = call.arguments as? [String: Any],
              let text = args["text"] as? String,
              let html = args["html"] as? String else {
          result(FlutterError(code: "bad_args",
                              message: "writeRich needs {text, html}",
                              details: nil))
          return
        }
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(text, forType: .string)
        pb.setString(html, forType: .html)
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }

    super.awakeFromNib()
  }

  // 窗口（重新）激活时的焦点恢复：捕获此刻的第一响应者，等 Flutter
  // 引擎自己的激活处理落定后强制归还。
  //
  // 一举两得：
  //   * Flutter 文本框：切走再切回后 NSTextInputClient 绑定可能已断开
  //     （光标还闪但按键被吞，实测 Flutter 3.47 仍存在），恢复
  //     FlutterView 即重建绑定 —— 与最初那版「无条件抢回 FlutterView」
  //     的 workaround 等效；
  //   * 平台视图（笔记编辑器 WKWebView）：焦点归还 webview —— 引擎的
  //     激活处理会把 FR 抢回 FlutterView，导致编辑器间歇性键盘失效
  //     （打字/Cmd+A 全无，再点一次才恢复）。
  //
  // 第一响应者不是本窗口的 view（为窗口本身/nil）时退回 FlutterView，
  // 与系统默认一致。
  // becomeMain 不处理：sheet 关闭等路径随后都会伴随 becomeKey。
  override func becomeKey() {
    super.becomeKey()
    let target: NSView? = {
      if let fr = firstResponder as? NSView, fr.window === self { return fr }
      return contentViewController?.view
    }()
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { [weak self] in
      guard let self, let target, target.window === self, self.isKeyWindow else { return }
      // makeFirstResponder(已是 FR 的对象) 是 no-op，先撤再设强制完整
      // resign/become 周期 —— WebKit 需要真正的 becomeFirstResponder
      // （且窗口已是 key）才恢复页面焦点（document.hasFocus）。
      self.makeFirstResponder(nil)
      self.makeFirstResponder(target)
    }
  }

  /// 命中测试窗口内所有可见 WKWebView（笔记编辑器、apps 面板可能同时
  /// 存在），返回包含该窗口坐标点的那个。
  private func webView(at windowPoint: NSPoint) -> WKWebView? {
    guard let root = contentViewController?.view else { return nil }
    return findWebViews(in: root).first {
      !$0.isHidden && $0.convert($0.bounds, to: nil).contains(windowPoint)
    }
  }

  private func findWebViews(in view: NSView) -> [WKWebView] {
    var out: [WKWebView] = []
    if let wv = view as? WKWebView { out.append(wv) }
    for sub in view.subviews {
      out.append(contentsOf: findWebViews(in: sub))
    }
    return out
  }
}
