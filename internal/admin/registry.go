package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
)

// RegistryAPI reads the v2 registry service's operator status
// (GET /internal/status, bearer REGISTRY_ADMIN_KEY). Read-only by design: the
// registry is its own service, and the website neither imports its code nor
// changes its state — publishing and withdrawing happen there.
type RegistryAPI struct {
	URL, Key string
}

// NewRegistryAPI returns nil when the registry is not configured.
func NewRegistryAPI(baseURL, key string) *RegistryAPI {
	if baseURL == "" || key == "" {
		return nil
	}
	return &RegistryAPI{URL: strings.TrimRight(baseURL, "/"), Key: key}
}

// registryStatus is the agreed shape of /internal/status. Fields the service
// adds later are ignored, so it can grow without this page breaking.
type registryStatus struct {
	Channels []struct {
		Name      string    `json:"name"`
		Revision  string    `json:"revision"`
		Sequence  int64     `json:"sequence"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"channels"`
	Revisions []struct {
		ID          string     `json:"id"`
		PolicyID    string     `json:"policy_id"`
		CreatedAt   time.Time  `json:"created_at"`
		WithdrawnAt *time.Time `json:"withdrawn_at"`
	} `json:"revisions"`
	Clients []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"clients"`
}

func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	var st registryStatus
	var errMsg string
	if s.Registry != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := getJSON(ctx, s.Registry.URL+"/internal/status", s.Registry.Key, &st); err != nil {
			s.log.Error("registry status", "err", err)
			errMsg = err.Error()
		}
	}
	s.render(w, http.StatusOK, "admin_registry.html", struct {
		base
		Configured bool
		Status     registryStatus
		Error      string
	}{s.page(r, ws, i18n.T("en", "admin.registry")), s.Registry != nil, st, errMsg})
}
