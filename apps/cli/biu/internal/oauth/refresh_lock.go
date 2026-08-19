// 跨进程刷新锁（方案 D6）：两个 biu 进程同时刷新会重复消费轮转的
// refresh token，identity 的 RefreshReuseGrace（10s）只是兜底，这里
// 用 ~/.biu/auth.lock 文件锁做第一道防线。锁只防并发、不存秘密。
//
// 使用方（TokenProvider）必须在锁内 double-check：重读 store，若别
// 的进程已刷新出未过期 token 就直接用，避免重复 refresh。

package oauth

import (
	"os"
	"path/filepath"
	"sync"
)

// refreshLockMu 是进程内兜底互斥：文件锁防跨进程，这把 mutex 防同
// 进程多 goroutine（Flock 在同进程多 fd 下不互斥）。
var refreshLockMu sync.Mutex

// refreshLockPath 返回锁文件路径（~/.biu/auth.lock）。抽成变量方便
// 测试指到 t.TempDir()。
var refreshLockPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "auth.lock"), nil
}

// withRefreshLock 在跨进程互斥下执行 fn。锁获取失败不阻塞主流程
// 降级为仅进程内互斥——宁可冒一次重复刷新（服务端 grace 兜底），
// 也不让文件系统问题挡住发请求。
func withRefreshLock(fn func() error) error {
	refreshLockMu.Lock()
	defer refreshLockMu.Unlock()
	unlock, err := acquireFileLock()
	if err == nil {
		defer unlock()
	}
	return fn()
}
