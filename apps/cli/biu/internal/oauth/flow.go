// 浏览器登录 flow 的可复用封装：`biu auth login` 与 REPL `/login`
// 共用（方案 D8）——打印 URL、尝试开浏览器、跑 PKCE 监听、落盘。
// manual 粘贴流涉及终端逐行交互，仍留在 cmd 层。

package oauth

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

// BrowserLogin 执行 automatic PKCE flow 并把 tokens 存入 store。
// progress 接收用户可见的提示行（authorize URL、成功信息）；nil →
// 丢弃。返回 error 时调用方负责回落（如引导 --manual）。
func BrowserLogin(ctx context.Context, cfg Config, store *Store, progress io.Writer) error {
	if progress == nil {
		progress = io.Discard
	}
	login := Login{
		Config: cfg,
		UrlOpener: func(authURL string) {
			fmt.Fprintln(progress, "[biu] open this URL in your browser to log in:")
			fmt.Fprintln(progress, "      "+authURL)
			_ = OpenBrowser(authURL)
		},
	}
	res, err := login.Run(ctx)
	if err != nil {
		return err
	}
	if err := store.Save(res.Tokens); err != nil {
		return fmt.Errorf("oauth: save tokens: %w", err)
	}
	fmt.Fprintf(progress, "[biu] logged in — credentials stored in %s\n", store.Path())
	return nil
}

// OpenBrowser 尝试用 OS 默认浏览器打开 URL。失败不致命——URL 已
// 打印给用户，手贴也能完成登录。
func OpenBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
