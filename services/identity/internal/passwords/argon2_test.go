package passwords

import "testing"

// 测试用更小参数避免单测过慢
var testParams = Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashVerify(t *testing.T) {
	pw := "correct horse battery staple"
	h, err := Hash(pw, testParams)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Verify(pw, h)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Verify returned false for correct password")
	}
	bad, err := Verify("wrong", h)
	if err != nil {
		t.Fatal(err)
	}
	if bad {
		t.Fatal("Verify returned true for wrong password")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, malformed := range []string{
		"",
		"plain",
		"$argon2id$",
		"$argon2id$v=19$bad$x$y",
	} {
		if _, err := Verify("any", malformed); err == nil {
			t.Errorf("expected error for %q", malformed)
		}
	}
}
