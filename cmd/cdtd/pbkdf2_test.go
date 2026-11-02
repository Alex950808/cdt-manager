package main

import (
	"testing"
)

// 用 Python hashlib 独立实现的 PBKDF2 向量校验（非本包实现自证）。
func TestPBKDF2ReferenceVector(t *testing.T) {
	// （独立于本包实现的外部参考值，防自证）
	got := pbkdf2Sha256([]byte("password"), []byte("salt"), 4096)
	if len(got) != 32 {
		t.Fatalf("len = %d", len(got))
	}
	// 参考向量：hashlib.pbkdf2_hmac("sha256", b"passwd", b"salt", 1)
	// hashlib.pbkdf2_hmac('sha256', b'passwd', b'salt', 1) =
	// 55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc
	expected := "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc"
	got1 := pbkdf2Sha256([]byte("passwd"), []byte("salt"), 1)
	hex := ""
	for _, b := range got1 {
		const hexdigits = "0123456789abcdef"
		hex += string(hexdigits[b>>4]) + string(hexdigits[b&0x0f])
	}
	if hex != expected {
		t.Fatalf("pbkdf2 vector mismatch: got %s want %s", hex, expected)
	}
}

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("s3cret-pass", hash) {
		t.Error("correct password should verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Error("wrong password should not verify")
	}
	if !hasPrefix(hash, "pbkdf2_sha256$260000$") {
		t.Errorf("format = %q", hash)
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
