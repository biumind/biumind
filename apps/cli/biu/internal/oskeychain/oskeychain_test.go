package oskeychain

import "testing"

type stub struct{}

func (stub) Name() string                             { return "stub" }
func (stub) Get(string, string) (string, bool, error) { return "", false, nil }
func (stub) Set(string, string, string) error         { return nil }
func (stub) Delete(string, string) error              { return nil }

func TestOpen_HonoursTestOverride(t *testing.T) {
	// 注入 stub → Open 返回它 + true。
	restore := SetForTest(stub{})
	k, ok := Open()
	if !ok || k == nil || k.Name() != "stub" {
		t.Fatalf("override not honoured: ok=%v k=%v", ok, k)
	}
	restore()

	// 注入 nil → 模拟无 keychain，Open 返回 (nil,false)。
	restore = SetForTest(nil)
	if k, ok := Open(); ok || k != nil {
		t.Fatalf("nil override should report unavailable, got ok=%v k=%v", ok, k)
	}
	restore()
}
