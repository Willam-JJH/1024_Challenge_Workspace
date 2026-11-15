package broker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishSkipsFetchWithoutSubscribers(t *testing.T) {
	var calls int32
	b := New(time.Second, func(context.Context) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte("frame"), nil
	})

	b.publish(context.Background())
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("没有订阅者时不该查库，实际查了 %d 次", got)
	}
}

func TestPublishDeliversToSubscribers(t *testing.T) {
	b := New(time.Second, func(context.Context) ([]byte, error) {
		return []byte("frame-1"), nil
	})
	updates, cancel := b.Subscribe()
	defer cancel()

	b.publish(context.Background())

	select {
	case frame := <-updates:
		if string(frame) != "frame-1" {
			t.Fatalf("收到 %q", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者没有收到帧")
	}
}

func TestSlowSubscriberKeepsLatestFrame(t *testing.T) {
	var version int32
	b := New(time.Second, func(context.Context) ([]byte, error) {
		n := atomic.AddInt32(&version, 1)
		return []byte{byte('0' + n)}, nil
	})
	updates, cancel := b.Subscribe()
	defer cancel()

	// 连推三帧但不读，模拟慢客户端。
	for i := 0; i < 3; i++ {
		b.publish(context.Background())
	}

	got := <-updates
	if string(got) != "3" {
		t.Fatalf("慢客户端应当只留最新一帧（3），得到 %q", got)
	}
}

func TestUnsubscribeRemovesSubscriber(t *testing.T) {
	b := New(time.Second, func(context.Context) ([]byte, error) { return []byte("x"), nil })
	_, cancel := b.Subscribe()
	if b.Subscribers() != 1 {
		t.Fatalf("订阅后应有 1 个订阅者，得到 %d", b.Subscribers())
	}
	cancel()
	if b.Subscribers() != 0 {
		t.Fatalf("退订后应为 0，得到 %d", b.Subscribers())
	}
	// 重复退订不应 panic（cancel 内部用 sync.Once 保护）。
	cancel()
}

func TestPublishIgnoresFetchError(t *testing.T) {
	b := New(time.Second, func(context.Context) ([]byte, error) {
		return nil, errors.New("数据库挂了")
	})
	updates, cancel := b.Subscribe()
	defer cancel()

	b.publish(context.Background())

	if _, ok := b.Snapshot(); ok {
		t.Fatal("取数失败时不应产生新帧")
	}
	select {
	case frame := <-updates:
		t.Fatalf("取数失败时不该推送，却收到 %q", frame)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSnapshotClearedWhenLastSubscriberLeaves(t *testing.T) {
	b := New(time.Second, func(context.Context) ([]byte, error) { return []byte("old"), nil })
	_, cancel := b.Subscribe()
	b.publish(context.Background())
	if _, ok := b.Snapshot(); !ok {
		t.Fatal("推送后应能取到快照")
	}

	cancel()
	b.publish(context.Background())

	if frame, ok := b.Snapshot(); ok {
		t.Fatalf("无订阅者时快照应作废，却拿到 %q", frame)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	b := New(10*time.Millisecond, func(context.Context) ([]byte, error) { return []byte("x"), nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 Run 应当退出")
	}
}
