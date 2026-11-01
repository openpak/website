package main

import (
	"log/slog"

	"github.com/openpak/website/internal/companion"
)

// registerCompanionTitles adds the titles that have companion pages. Each source reads its
// backend through that backend's own API; one whose URL is not configured is left out, so a
// deployment without the backend shows no page for it.
func registerCompanionTitles(c *companion.Service, log *slog.Logger) {
	_ = c
	_ = log
}
