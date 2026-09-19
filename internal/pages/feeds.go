package pages

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// siteURL makes feed links absolute; openpak.org is the only host the site answers on.
const siteURL = "https://openpak.org"

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}

type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Content string   `xml:"content,omitempty"`
}

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Author  string      `xml:"author>name"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

// writeFeed sends an Atom feed; the five-minute max-age lets the CDN absorb
// feed readers polling.
func writeFeed(w http.ResponseWriter, path, title, page string, entries []atomEntry) {
	f := atomFeed{Title: title + " · OpenPak", ID: siteURL + path, Author: "OpenPak",
		Links:   []atomLink{{Href: siteURL + path, Rel: "self"}, {Href: siteURL + page}},
		Updated: time.Unix(0, 0).UTC().Format(time.RFC3339), Entries: entries}
	if len(entries) > 0 {
		f.Updated = entries[0].Updated
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(f)
}

// handleNewsFeed is /feed/news.xml: the published articles the landing page shows.
func (s *Server) handleNewsFeed(w http.ResponseWriter, r *http.Request) {
	var entries []atomEntry
	for _, a := range s.News.Latest(r.Context(), 20) {
		entries = append(entries, atomEntry{Title: a.Title, ID: siteURL + "/news/" + a.Slug,
			Updated: a.Published.UTC().Format(time.RFC3339), Link: atomLink{Href: siteURL + "/"}, Content: a.Body})
	}
	writeFeed(w, "/feed/news.xml", "News", "/", entries)
}

// handleTitlesFeed is /feed/titles.xml: title status changes, read from the
// title.status audit rows. Notes and the acting admin stay out.
func (s *Server) handleTitlesFeed(w http.ResponseWriter, r *http.Request) {
	// ponytail: scans the newest 500 audit rows of every kind; filter in SQL if other actions crowd title.status out.
	audit, err := s.DB.ListAudit(r.Context(), 500)
	if err != nil {
		s.log.Error("titles feed", "err", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var entries []atomEntry
	for _, e := range audit {
		if e.Action != "title.status" || len(entries) == 50 {
			continue
		}
		// detail is "<id> <name>: <old> -> <new>[ notes=...]"
		d, _, _ := strings.Cut(e.Detail, " notes=")
		_, d, _ = strings.Cut(d, " ")
		entries = append(entries, atomEntry{Title: d, ID: siteURL + "/feed/titles.xml#" + strconv.FormatInt(e.ID, 10),
			Updated: e.CreatedAt.UTC().Format(time.RFC3339), Link: atomLink{Href: siteURL + "/titles"}})
	}
	writeFeed(w, "/feed/titles.xml", "Title status", "/titles", entries)
}
