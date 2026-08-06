// VerificationThrottle — 限制每邮箱的 resend 频次, 抵抗滥用 + 减少 SMTP 成本.
//
// 跟 LoginThrottle 思路相同 (内存滑动窗口 + 多实例容忍), 但用途是"拒绝请求"
// 而非"事后告警", 所以 Allow 直接返 bool, 调用方据此返 429.
//
// 两层窗口:
//   - cooldown (60s): 上一次发送后短期内不准再发, 防止用户连点
//   - daily cap (5/24h): 防自动化刷量
//
// verify 失败次数限制走 store 的 attempts 字段, 不在这里处理.

package api

import (
	"sync"
	"time"
)

type VerificationThrottle struct {
	Cooldown time.Duration // 60s
	DailyCap int           // 5
	Window   time.Duration // 24h (DailyCap 的窗口)

	mu    sync.Mutex
	sends map[string][]time.Time // email → 发送时间戳
}

func NewVerificationThrottle() *VerificationThrottle {
	return &VerificationThrottle{
		Cooldown: 60 * time.Second,
		DailyCap: 5,
		Window:   24 * time.Hour,
		sends:    make(map[string][]time.Time),
	}
}

// AllowAndRecord 原子地"判断是否能发 + 记录已发". 返回:
//   - allow=true: 调用方继续发邮件
//   - allow=false: 调用方返 429 + retryAfter (距下一次可发的秒数)
//
// 不允许时不记录 — 让攻击者不能通过空 ping 把自己锁更久.
func (t *VerificationThrottle) AllowAndRecord(email string) (allow bool, retryAfter time.Duration) {
	if email == "" {
		return false, 0
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	hist := t.sends[email]
	cutoff := now.Add(-t.Window)
	pruned := hist[:0]
	for _, ts := range hist {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}

	// cooldown: 距上一次 < Cooldown 直接拒
	if len(pruned) > 0 {
		last := pruned[len(pruned)-1]
		if d := now.Sub(last); d < t.Cooldown {
			t.sends[email] = pruned // 仍把 prune 的写回, 顺便清理
			return false, t.Cooldown - d
		}
	}

	// daily cap
	if len(pruned) >= t.DailyCap {
		// 距 24h 窗口最早一条多远才能再发 (= 它何时过期)
		earliest := pruned[0]
		t.sends[email] = pruned
		return false, t.Window - now.Sub(earliest)
	}

	pruned = append(pruned, now)
	t.sends[email] = pruned
	return true, 0
}

// Reset — 验证成功后清掉该 email 的发送记录, 让用户日后再注册不卡 cap.
// 实际不会有人对同邮箱注册第二次 (UNIQUE), 但 Reset 让测试 / 重置场景干净.
func (t *VerificationThrottle) Reset(email string) {
	if email == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sends, email)
}
