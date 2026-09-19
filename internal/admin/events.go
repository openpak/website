package admin

import (
	"context"
	"slices"

	"github.com/openpak/website/internal/core"
)

// eventPage is the core's own cap on one PollEvents page.
const eventPage = 1000

// eventHead walks the core's event stream forward from a hint and returns the
// newest version. The core only pages forward — PollEvents is oldest-first and
// its max_version is the page's, not the stream's — so there is no cheaper way
// to learn the head. The walk is capped: past that the answer is a lower bound,
// which is still more useful than a page that never loads.
func eventHead(ctx context.Context, ev core.Events, from uint64) (uint64, error) {
	head := from
	for range 100 {
		page, max, err := ev.Poll(ctx, head, eventPage)
		if err != nil {
			return head, err
		}
		if max > head {
			head = max
		}
		if len(page) < eventPage {
			break
		}
	}
	return head, nil
}

// newestEvents returns the last keep events, newest first, and the head
// version. Versions come from a counter bumped inside the emitting
// transaction, so they are dense and head-keep is exactly where the last keep
// begin.
func newestEvents(ctx context.Context, ev core.Events, hint uint64, keep int) ([]core.Event, uint64, error) {
	head, err := eventHead(ctx, ev, hint)
	if err != nil {
		return nil, head, err
	}
	since := uint64(0)
	if head > uint64(keep) {
		since = head - uint64(keep)
	}
	events, _, err := ev.Poll(ctx, since, int32(keep))
	slices.Reverse(events)
	return events, head, err
}
