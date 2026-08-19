//go:build !windows

package oauth

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// acquireFileLock 以 flock(LOCK_EX) 抢 ~/.biu/auth.lock。返回的
// unlock 关闭 fd 即放锁（flock 随 fd 关闭释放）。
func acquireFileLock() (func(), error) {
	p, err := refreshLockPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
