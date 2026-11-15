// Package broker 把「每条连接各查一次数据库」换成「一个轮询器 + 多订阅者」。
//
// 用法：每类实时数据一个 Broker。Run 启动轮询，Subscribe 拿通道。
// 没有订阅者时不查库——站点空闲时不该有人一直打数据库。
package broker

import (
	"context"
	"sync"
	"time"
)

// Broker 是一个定时取数并广播给所有订阅者的对象。
type Broker struct {
	interval time.Duration
	fetch    func(context.Context) ([]byte, error)

	mu     sync.RWMutex
	latest []byte
	ready  bool
	subs   map[chan []byte]struct{}
}

// New 构造一个 Broker。fetch 返回可直接下发的一帧内容。
func New(interval time.Duration, fetch func(context.Context) ([]byte, error)) *Broker {
	return &Broker{
		interval: interval,
		fetch:    fetch,
		subs:     make(map[chan []byte]struct{}),
	}
}

// Run 启动轮询，直到 ctx 结束。
func (b *Broker) Run(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.publish(ctx)
		}
	}
}

// Snapshot 返回最近一帧。新连接可以先拿它，不用等下一个周期。
func (b *Broker) Snapshot() ([]byte, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.ready {
		return nil, false
	}
	return b.latest, true
}

// Subscribe 返回一个只保留最新一帧的通道，以及用于退订的函数。
func (b *Broker) Subscribe() (<-chan []byte, func()) {
	channel := make(chan []byte, 1)
	b.mu.Lock()
	b.subs[channel] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, channel)
			b.mu.Unlock()
		})
	}
	return channel, cancel
}

// Subscribers 返回当前订阅数，供测试与自检使用。
func (b *Broker) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func (b *Broker) publish(ctx context.Context) {
	b.mu.RLock()
	count := len(b.subs)
	b.mu.RUnlock()
	if count == 0 {
		// 没人看就不查库，同时把旧帧作废，避免下一个订阅者拿到陈旧内容。
		b.mu.Lock()
		b.latest, b.ready = nil, false
		b.mu.Unlock()
		return
	}

	body, err := b.fetch(ctx)
	if err != nil || body == nil {
		// 取数失败就跳过这一轮：推半截数据比不推更糟。
		return
	}

	b.mu.Lock()
	b.latest, b.ready = body, true
	b.mu.Unlock()

	b.mu.RLock()
	defer b.mu.RUnlock()
	for channel := range b.subs {
		// 通道容量是 1：满了就先丢旧的再放新的。
		// 订阅者要的是「最新状态」，中间帧被丢弃不会丢信息。
		select {
		case channel <- body:
		default:
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- body:
			default:
			}
		}
	}
}
