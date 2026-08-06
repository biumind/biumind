import Cocoa
import FlutterMacOS

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

    super.awakeFromNib()
  }

  // Workaround for flutter/flutter#123961 — when the user switches
  // away from the app and back (Cmd-Tab, clicking the dock icon,
  // alt-clicking from another window), Flutter's text input client
  // sometimes doesn't re-engage. Visually the TextField still shows
  // a blinking cursor but key events go nowhere — only certain
  // keystrokes (notably bare letters via the Cocoa fallback
  // text-insertion path) make it through; numbers, punctuation, and
  // IME events get swallowed.
  //
  // Forcing the FlutterView back to first responder every time the
  // window becomes key restores the NSTextInputClient binding that
  // FlutterTextInputPlugin relies on. The cost is O(1) per focus
  // gain, which is invisible.
  override func becomeKey() {
    super.becomeKey()
    if let view = contentViewController?.view {
      makeFirstResponder(view)
    }
  }

  // Same fix for the rarer "becomes main without becoming key" path
  // (e.g. when a sheet was on top and then gets dismissed).
  override func becomeMain() {
    super.becomeMain()
    if let view = contentViewController?.view {
      makeFirstResponder(view)
    }
  }
}
