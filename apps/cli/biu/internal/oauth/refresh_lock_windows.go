//go:build windows

package oauth

// Windows 上没有对应的 flock 语义（golang.org/x/sys/windows 的
// LockFileEx 需要缠 OVERLAPPED，复杂度不值）——降级为仅进程内
// 互斥（withRefreshLock 里的 refreshLockMu），跨进程并发刷新交给
// identity 的 RefreshReuseGrace 兜底。kimi-code 在 Windows 同样跳锁。
func acquireFileLock() (func(), error) {
	return func() {}, nil
}
