package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetCachesWithinTTL(t *testing.T) {
	c := New()
	calls := 0
	fetch := func() (any, error) { calls++; return calls, nil }

	first := c.Get("k", time.Minute, fetch)
	second := c.Get("k", time.Minute, fetch)
	if calls != 1 || first.Value != 1 || second.Value != 1 || second.Stale || second.Err != nil {
		t.Fatalf("calls=%d first=%+v second=%+v", calls, first, second)
	}
	if c.Get("other", time.Minute, fetch).Value != 2 {
		t.Error("keys must not share entries")
	}
}

func TestGetRefetchesAfterTTL(t *testing.T) {
	c := New()
	calls := 0
	fetch := func() (any, error) { calls++; return calls, nil }
	c.Get("k", time.Nanosecond, fetch)
	time.Sleep(time.Millisecond)
	if res := c.Get("k", time.Nanosecond, fetch); res.Value != 2 {
		t.Fatalf("value = %v", res.Value)
	}
}

func TestGetServesStaleValueWhenRefreshFails(t *testing.T) {
	c := New()
	boom := errors.New("boom")
	c.Get("k", time.Nanosecond, func() (any, error) { return "good", nil })
	time.Sleep(time.Millisecond)

	res := c.Get("k", time.Nanosecond, func() (any, error) { return nil, boom })
	if res.Value != "good" || !res.Stale || !errors.Is(res.Err, boom) || res.FetchedAt.IsZero() {
		t.Fatalf("%+v", res)
	}
}

func TestGetBacksOffAfterAnError(t *testing.T) {
	c := New()
	calls := 0
	fail := func() (any, error) { calls++; return nil, errors.New("down") }

	if res := c.Get("k", time.Minute, fail); res.Err == nil || res.Value != nil {
		t.Fatalf("%+v", res)
	}
	if res := c.Get("k", time.Minute, fail); res.Err == nil {
		t.Fatalf("%+v", res)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times during the backoff window", calls)
	}
}

func TestGetSharesOneFetchBetweenConcurrentCallers(t *testing.T) {
	c := New()
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Get("k", time.Minute, func() (any, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "v", nil
			})
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("fetch called %d times", calls.Load())
	}
}
