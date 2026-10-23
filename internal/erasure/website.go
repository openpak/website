package erasure

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/openpak/website/internal/attachments"
	"github.com/openpak/website/internal/media"
	"github.com/openpak/website/internal/store"
)

// WebsiteStep erases what the website holds: ticket and crash-report files
// first (their keys live in the rows), avatar files, then every row. A nil
// files or avatars means this deployment keeps none; it is logged, not fatal.
func WebsiteStep(db store.ErasureStore, files *attachments.Store, avatars *media.Service, log *slog.Logger) Step {
	return Step{Name: "website", Erase: func(ctx context.Context, id string) error {
		keys, err := db.AttachmentKeysForAccount(ctx, id)
		if err != nil {
			return err
		}
		if files == nil && len(keys) > 0 {
			log.Warn("erasure: attachment storage is not configured; files cannot be deleted", "account", id, "files", len(keys))
		}
		if files != nil {
			for _, k := range keys {
				if err := files.Delete(k); err != nil {
					return fmt.Errorf("attachment %s: %w", k, err)
				}
			}
		}
		if avatars != nil {
			if err := avatars.Erase(ctx, id); err != nil {
				return err
			}
		}
		return db.EraseAccount(ctx, id)
	}}
}
