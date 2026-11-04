package auth

import "testing"

// 由 Python 实现 app/core/security.py 生成，用于验证跨语言兼容：
// hash_password("bench-password-123")。
const pythonStoredHash = "scrypt$16384$8$1$5132aab86f85d2bf6bd9fe4063362c2f$92836f2b9a41381f740dcd660469c1ba9017c6e065e9bb9155a8ebb2ce8b48cc"

func TestVerifyPasswordMatchesPython(t *testing.T) {
	if !VerifyPassword("bench-password-123", pythonStoredHash) {
		t.Fatal("Python 生成的哈希在 Go 侧校验失败")
	}
	if VerifyPassword("wrong-password", pythonStoredHash) {
		t.Fatal("错误口令不应通过")
	}
	if VerifyPassword("bench-password-123", "plain$text") {
		t.Fatal("非法存储格式不应通过")
	}
}

func TestHashPasswordRoundTrip(t *testing.T) {
	stored, err := HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !VerifyPassword("s3cret-pass", stored) {
		t.Fatal("自产哈希校验失败")
	}
	if VerifyPassword("s3cret-pas", stored) {
		t.Fatal("相近口令不应通过")
	}
}

func TestHashTokenMatchesPython(t *testing.T) {
	// hashlib.sha256(b"test-token").hexdigest()
	const want = "4c5dc9b7708905f77f5e5d16316b5dfb425e68cb326dcd55a860e90a7707031e"
	if got := HashToken("test-token"); got != want {
		t.Fatalf("HashToken = %s, want %s", got, want)
	}
}

func TestNewTokenShape(t *testing.T) {
	token, err := NewToken("sk_")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	// secrets.token_urlsafe(32) 产生 43 个字符。
	if len(token) != len("sk_")+43 {
		t.Fatalf("len = %d, want %d", len(token), len("sk_")+43)
	}
	other, err := NewToken("")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if other == token[len("sk_"):] {
		t.Fatal("两次生成的令牌不应相同")
	}
}

func TestNewIDShape(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("id: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("len = %d, want 32", len(id))
	}
}
