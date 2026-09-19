package admin

import (
	"context"
	"slices"

	"github.com/openpak/website/internal/core"
)

// newestEvents returns the last keep events, newest first, and the head
// version. Versions come from a counter bumped inside the emitting
// transaction, so they are dense and head-keep is exactly where the last keep
// begin.
func newestEvents(ctx context.Context, ev core.Events, hint uint64, keep int) ([]core.Event, uint64, error) {
	head, err := core.EventHead(ctx, ev, hint)
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
