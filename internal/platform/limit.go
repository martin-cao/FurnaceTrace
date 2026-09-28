package platform

import (
	"sync"
	"time"
)

// Limiter bounds only small, sensitive operations such as login and pairing.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]limitEntry
}
type limitEntry struct {
	count int
	until time.Time
}

func (l *Limiter) Allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.entries == nil {
		l.entries = make(map[string]limitEntry)
	}
	if len(l.entries) > 1000 {
		for k, v := range l.entries {
			if now.After(v.until) {
				delete(l.entries, k)
			}
		}
	}
	v := l.entries[key]
	if now.After(v.until) {
		v = limitEntry{until: now.Add(window)}
	}
	if v.count >= max {
		return false
	}
	v.count++
	l.entries[key] = v
	return true
}
