package admin

import (
	"context"
	"testing"

	"github.com/openpak/website/internal/core"
)

// stream behaves like the core's PollEvents: oldest first, capped pages, and
// max_version is the page's highest, not the stream's.
type stream struct{ n uint64 }

func (s *stream) Poll(_ context.Context, since uint64, limit int32) ([]core.Event, uint64, error) {
	var out []core.Event
	for v := since + 1; v <= s.n && len(out) < int(limit); v++ {
		out = append(out, core.Event{Version: v})
	}
	max := since
	if len(out) > 0 {
		max = out[len(out)-1].Version
	}
	return out, max, nil
}

func TestNewestEventsIsTheTail(t *testing.T) {
	for _, hint := range []uint64{0, 2400} {
		got, head, err := newestEvents(context.Background(), &stream{n: 2500}, hint, 200)
		if err != nil {
			t.Fatal(err)
		}
		if head != 2500 || len(got) != 200 || got[0].Version != 2500 || got[199].Version != 2301 {
			t.Fatalf("hint %d: head=%d len=%d first=%v", hint, head, len(got), got[0].Version)
		}
	}
}
