package pages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/netmap"
)

// The public network map on /status: consoles, the services each uses, and the
// shared core, with live status. It is derived from the same graph as the staff
// map (/admin/network) but through netmap.Public, which drops every address,
// port, repository, database and server name. The browser draws it from
// /status/map.json.

// handleNetMapJSON answers the sanitised graph. Cached for the graph's own TTL;
// nothing in it is per-visitor.
func (s *Server) handleNetMapJSON(w http.ResponseWriter, r *http.Request) {
	if s.NetMap == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=15")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	lang := i18n.FromRequest(r)
	tr := func(key string, args ...any) string { return fmt.Sprintf(i18n.T(lang, "netmap."+key), args...) }
	w.Header().Set("Vary", "Accept-Language, Cookie")
	_ = json.NewEncoder(w).Encode(netmap.Public(s.NetMap.Graph(r.Context()), tr))
}

// ConsoleStages is the per-console stage the feature rows give, for the map.
func (s *Server) ConsoleStages(ctx context.Context) map[string]string {
	return s.consoleStages(ctx)
}

// netmapStrings is the JSON the map script reads from data-strings, in the
// visitor's language.
func netmapStrings(lang string) string {
	t := func(k string) string { return i18n.T(lang, "netmap."+k) }
	b, _ := json.Marshal(map[string]any{
		"search": t("search"), "searchPh": t("searchph"), "network": t("network"), "allNetworks": t("allnetworks"),
		"status": t("status"), "all": t("all"), "problems": t("problems"), "downOnly": t("down"), "upOnly": t("up"),
		"map": t("map"), "list": t("list"), "fit": t("fit"), "zoomIn": t("zoomin"), "zoomOut": t("zoomout"),
		"hint": t("hint"), "noMatch": t("nomatch"), "close": t("close"), "members": t("members"), "usedBy": t("usedby"),
		"uses": t("uses"), "colConsoles": t("col.consoles"), "colUse": t("col.use"), "colShared": t("col.shared"),
		"listShared": t("list.shared"), "listSites": t("list.sites"), "loading": t("loading"), "failed": t("failed"), "updated": t("updated"), "legend": t("legend"),
		"words": map[string]string{"up": t("w.up"), "degraded": t("w.degraded"), "down": t("w.down"), "idle": t("w.idle"),
			"planned": t("w.planned"), "unknown": t("w.unknown")},
	})
	return string(b)
}
