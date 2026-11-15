package api

import (
	"testing"
	"time"
)

func TestSlidingWindowAllowsUpToLimit(t *testing.T) {
	window := newSlidingWindow()
	for index := 0; index < 3; index++ {
		ok, retry := window.allow("k", 3, time.Minute)
		if !ok || retry != 0 {
			t.Fatalf("第 %d 次应放行，得到 ok=%v retry=%d", index+1, ok, retry)
		}
	}

	ok, retry := window.allow("k", 3, time.Minute)
	if ok {
		t.Fatal("第 4 次应被拒绝")
	}
	if retry < 1 || retry > 60 {
		t.Fatalf("Retry-After 应在 1..60，得到 %d", retry)
	}

	if ok, _ := window.allow("other", 3, time.Minute); !ok {
		t.Fatal("不同 key 不应互相影响")
	}
}

func TestSlidingWindowExpires(t *testing.T) {
	window := newSlidingWindow()
	if ok, _ := window.allow("k", 1, 50*time.Millisecond); !ok {
		t.Fatal("第一次应放行")
	}
	if ok, _ := window.allow("k", 1, 50*time.Millisecond); ok {
		t.Fatal("窗口内第二次应被拒绝")
	}
	time.Sleep(60 * time.Millisecond)
	if ok, _ := window.allow("k", 1, 50*time.Millisecond); !ok {
		t.Fatal("窗口过后应重新放行")
	}
}

func TestSlidingWindowDisabledWhenLimitIsZero(t *testing.T) {
	window := newSlidingWindow()
	for index := 0; index < 5; index++ {
		if ok, _ := window.allow("k", 0, time.Minute); !ok {
			t.Fatal("limit 为 0 时应始终放行")
		}
	}
}
