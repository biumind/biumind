import Cocoa
import FlutterMacOS

@main
class AppDelegate: FlutterAppDelegate {
  override func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    return true
  }

  override func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
    return true
  }

  // Dev workflow fix: when launched from a terminal via `flutter run`,
  // the terminal stays the active app and our NSWindow shows up but is
  // never the key window — Flutter's TextField shows a blinking cursor
  // (focus is set on the engine side) yet keyboard events go nowhere
  // and clicks are eaten as the "first mouse" activate gesture. Force
  // activation on launch so the window is immediately interactive.
  override func applicationDidFinishLaunching(_ notification: Notification) {
    super.applicationDidFinishLaunching(notification)
    NSApp.activate(ignoringOtherApps: true)
  }
}
