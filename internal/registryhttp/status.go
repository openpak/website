package registryhttp

import (
	"crypto/subtle"
	"net/http"
	"strconv"
)

// statusRevisionLimit bounds the revision list: the admin page shows what
// happened recently, not the audit log.
const statusRevisionLimit = 50

// revisionID names one revision row. The table has no id column; its
// primary key is (platform, channel, sequence), so that triple is the id,
// and a channel's "revision" is the same triple for the row it points at.
func revisionID(platform, channel string, sequence int64) string {
	return platform + "/" + channel + "/" + strconv.FormatInt(sequence, 10)
}

// handleInternalStatus is the operator's read-only view of the registry,
// consumed by the website's admin page. The key is a bearer secret compared
// in constant time; without REGISTRY_ADMIN_KEY the route does not exist, so a
// deployment that never set one refuses everybody rather than anybody.
func (s *Server) handleInternalStatus(w http.ResponseWriter, r *http.Request) {
	if s.AdminKey == "" {
		http.NotFound(w, r)
		return
	}
	got := r.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+s.AdminKey)) != 1 {
		writeV2Error(w, http.StatusUnauthorized, "unauthorized", "admin key rejected")
		return
	}
	if s.Registry == nil {
		writeV2Error(w, http.StatusServiceUnavailable, "unavailable", "network registry is not configured")
		return
	}
	ctx := r.Context()
	store := s.Registry.Store
	channels, err := store.ListChannels(ctx)
	if err != nil {
		s.log.Error("status: channels", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	revisions, err := store.ListRevisions(ctx, statusRevisionLimit)
	if err != nil {
		s.log.Error("status: revisions", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	clients, err := store.ListClients(ctx)
	if err != nil {
		s.log.Error("status: clients", "err", err)
		writeV2Error(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Empty lists encode as [], never null: the admin page iterates them.
	outChannels := make([]map[string]any, 0, len(channels))
	for _, c := range channels {
		outChannels = append(outChannels, map[string]any{
			// A channel is keyed by (platform, channel); there is no single
			// name column, so the name is the pair.
			"name":          c.Platform + "/" + c.Channel,
			"revision":      revisionID(c.Platform, c.Channel, c.Sequence),
			"sequence":      c.Sequence,
			"updated_at":    c.UpdatedAt.UTC().Format(v2RFC3339),
			"platform":      c.Platform,
			"channel":       c.Channel,
			"bundle_sha256": c.BundleSHA256,
		})
	}
	outRevisions := make([]map[string]any, 0, len(revisions))
	for _, rev := range revisions {
		var withdrawn any
		if rev.WithdrawnAt != nil {
			withdrawn = rev.WithdrawnAt.UTC().Format(v2RFC3339)
		}
		outRevisions = append(outRevisions, map[string]any{
			"id":        revisionID(rev.Platform, rev.Channel, rev.Sequence),
			"policy_id": rev.PolicyID,
			// The row records issued_at, the moment the revision was
			// published; that is its creation time.
			"created_at":      rev.IssuedAt.UTC().Format(v2RFC3339),
			"withdrawn_at":    withdrawn,
			"platform":        rev.Platform,
			"channel":         rev.Channel,
			"sequence":        rev.Sequence,
			"bundle_sha256":   rev.BundleSHA256,
			"manifest_sha256": rev.ManifestSHA256,
		})
	}
	outClients := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		outClients = append(outClients, map[string]any{
			"id": c.ID,
			// Registrations carry no display name; the repository is the
			// immutable identity they are known by.
			"name":        c.Repository,
			"status":      c.Status,
			"kind":        c.Kind,
			"environment": c.Environment,
		})
	}
	writeV2JSON(w, http.StatusOK, map[string]any{
		"channels":  outChannels,
		"revisions": outRevisions,
		"clients":   outClients,
	})
}
