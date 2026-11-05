package profilecache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadThrough(t *testing.T) {
	now := time.Unix(1000, 0)
	calls, fail := 0, false
	c := New(func(_ context.Context, id string) (Profile, error) {
		calls++
		if fail {
			return Profile{}, errors.New("core down")
		}
		return Profile{DisplayName: "n" + id, Email: "e@x"}, nil
	})
	c.now = func() time.Time { return now }
	ctx := context.Background()

	if p := c.Get(ctx, "s"); p.DisplayName != "ns" || calls != 1 {
		t.Fatalf("first read: %+v calls=%d", p, calls)
	}
	c.Get(ctx, "s")
	if calls != 1 {
		t.Fatalf("fresh entry refetched: %d", calls)
	}
	now = now.Add(61 * time.Second)
	fail = true
	if p := c.Get(ctx, "s"); p.DisplayName != "ns" || calls != 2 {
		t.Fatalf("stale on failure: %+v calls=%d", p, calls)
	}
	if p := c.Get(ctx, "other"); p != (Profile{}) {
		t.Fatalf("no cache + core down must be empty: %+v", p)
	}
	fail = false
	c.Invalidate("s")
	c.Get(ctx, "s")
	if calls != 4 {
		t.Fatalf("invalidate did not refetch: %d", calls)
	}
	var nilc *Cache
	if nilc.Get(ctx, "s") != (Profile{}) {
		t.Fatal("nil cache")
	}
}
