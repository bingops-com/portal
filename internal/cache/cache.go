// Package cache memoizes provider results per key with a TTL, serving the
// last good value when a refresh fails.
package cache

import (
	"sync"
	"time"
)

const (
	errorBackoff = 20 * time.Second
	maxStale     = 6 * time.Hour
)

type entry struct {
	mu      sync.Mutex
	val     any
	at      time.Time
	err     error
	errAt   time.Time
	touched time.Time
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]*entry
}

func New() *Cache {
	return &Cache{entries: map[string]*entry{}}
}

type Result struct {
	Value     any
	FetchedAt time.Time
	Stale     bool
	Err       error
}

// Get returns the cached value for key, calling fetch when it is older than
// ttl. Concurrent callers for the same key share one fetch.
func (c *Cache) Get(key string, ttl time.Duration, fetch func() (any, error)) Result {
	now := time.Now()
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		e = &entry{}
		c.entries[key] = e
	}
	e.touched = now
	if len(c.entries) > 512 {
		for k, v := range c.entries {
			if now.Sub(v.touched) > time.Hour {
				delete(c.entries, k)
			}
		}
	}
	c.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	now = time.Now()
	if !e.at.IsZero() && now.Sub(e.at) < ttl {
		return Result{Value: e.val, FetchedAt: e.at}
	}
	if e.err == nil || now.Sub(e.errAt) >= errorBackoff {
		val, err := fetch()
		if err == nil {
			e.val, e.at, e.err = val, time.Now(), nil
			return Result{Value: e.val, FetchedAt: e.at}
		}
		e.err, e.errAt = err, time.Now()
	}
	if !e.at.IsZero() && now.Sub(e.at) < maxStale {
		return Result{Value: e.val, FetchedAt: e.at, Stale: true, Err: e.err}
	}
	return Result{Err: e.err}
}
