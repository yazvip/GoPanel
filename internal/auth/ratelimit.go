package auth

import (
	"sync"
	"time"
)

type bucket struct {
	count    int
	resetAt  time.Time
	blocked  time.Time
}

type Limiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	buckets  map[string]*bucket
	lastSweep time.Time
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, buckets: map[string]*bucket{}, lastSweep: time.Now()}
}

func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.lastSweep) > 10*time.Minute {
		for k, b := range l.buckets {
			if now.After(b.resetAt) && now.After(b.blocked) {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		return true, 0
	}
	if now.Before(b.blocked) {
		return false, b.blocked.Sub(now)
	}
	if now.After(b.resetAt) {
		delete(l.buckets, key)
		return true, 0
	}
	return true, 0
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok || now.After(b.resetAt) {
		b = &bucket{resetAt: now.Add(l.window)}
		l.buckets[key] = b
	}
	b.count++
	if b.count >= l.limit {
		b.blocked = now.Add(l.window)
	}
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		return false, 0
	}
	if time.Now().Before(b.blocked) {
		return true, time.Until(b.blocked)
	}
	return false, 0
}
