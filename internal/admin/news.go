package admin

import (
	"net/http"
	"strings"

	"google.golang.org/grpc/status"

	"github.com/openpak/website/internal/i18n"
	newsv1 "github.com/openpak/website/internal/newspb"
	"github.com/openpak/website/internal/store"
)

// newsRow is one article in the list, reduced to the language this page edits.
type newsRow struct {
	ID, Slug, Title, Updated string
	Published                bool
}

// newsForm is the create/edit form. One language per article on this page;
// an article newsctl gave several keeps the others, because an update only
// upserts the locale it sends.
type newsForm struct {
	base
	ID, Slug, Platforms, Language, Heading, Body string
	Published                                    bool
	Error                                        string
}

// locale picks the language the form edits: English first, as the landing
// page does, then whatever the article has.
func locale(a *newsv1.Article) *newsv1.ArticleLocale {
	for _, l := range a.GetLocales() {
		if strings.HasPrefix(strings.ToLower(l.GetLanguage()), "en") {
			return l
		}
	}
	if ls := a.GetLocales(); len(ls) > 0 {
		return ls[0]
	}
	return &newsv1.ArticleLocale{Language: "en-US"}
}

// splitList reads a comma-separated field. Empty means every platform, which
// is how the news service spells "all" too.
func splitList(v string) []string {
	var out []string
	for p := range strings.SplitSeq(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// grpcMessage is what the news service said, without the transport prefix: it
// is shown to the operator who filled in the form.
func grpcMessage(err error) string { return status.Convert(err).Message() }

func (s *Server) newsOff(w http.ResponseWriter, r *http.Request, ws store.WebSession) {
	s.render(w, http.StatusOK, "admin_pending.html", struct {
		base
		Notice string
	}{s.page(r, ws, i18n.T("en", "admin.news")), i18n.T("en", "admin.news.off")})
}

func (s *Server) handleNews(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.News == nil {
		s.newsOff(w, r, ws)
		return
	}
	var rows []newsRow
	var errMsg string
	resp, err := s.News.ListArticles(r.Context(), &newsv1.ListArticlesRequest{IncludeUnpublished: true})
	if err != nil {
		s.log.Error("news list", "err", err)
		errMsg = grpcMessage(err)
	}
	for _, a := range resp.GetArticles() {
		rows = append(rows, newsRow{ID: a.GetId(), Slug: a.GetSlug(), Title: locale(a).GetTitle(),
			Updated: a.GetUpdatedAt(), Published: a.GetPublishedAt() != ""})
	}
	s.render(w, http.StatusOK, "admin_news.html", struct {
		base
		Articles []newsRow
		Error    string
	}{s.page(r, ws, i18n.T("en", "admin.news")), rows, errMsg})
}

func (s *Server) handleNewsNew(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.News == nil {
		s.newsOff(w, r, ws)
		return
	}
	s.render(w, http.StatusOK, "admin_news_edit.html", newsForm{base: s.page(r, ws, i18n.T("en", "admin.news.new")), Language: "en-US"})
}

func (s *Server) handleNewsEdit(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if s.News == nil {
		s.newsOff(w, r, ws)
		return
	}
	a, err := s.News.GetArticle(r.Context(), &newsv1.ArticleRef{Id: r.PathValue("id")})
	if err != nil {
		http.Error(w, grpcMessage(err), http.StatusNotFound)
		return
	}
	l := locale(a)
	s.render(w, http.StatusOK, "admin_news_edit.html", newsForm{
		base: s.page(r, ws, i18n.T("en", "admin.news.edit")),
		ID:   a.GetId(), Slug: a.GetSlug(), Platforms: strings.Join(a.GetAudience().GetPlatforms(), ", "),
		Language: l.GetLanguage(), Heading: l.GetTitle(), Body: l.GetBody(), Published: a.GetPublishedAt() != "",
	})
}

// handleNewsSave creates (no id in the path) or updates an article. A refusal
// from the news service re-renders the form with what was typed and why.
func (s *Server) handleNewsSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.News == nil {
		http.Error(w, i18n.T("en", "admin.news.off"), http.StatusServiceUnavailable)
		return
	}
	f := newsForm{
		ID: r.PathValue("id"), Slug: strings.TrimSpace(r.PostFormValue("slug")),
		Platforms: r.PostFormValue("platforms"), Language: strings.TrimSpace(r.PostFormValue("language")),
		Heading: strings.TrimSpace(r.PostFormValue("title")), Body: strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n"),
	}
	if f.Language == "" {
		f.Language = "en-US"
	}
	loc := []*newsv1.ArticleLocale{{Language: f.Language, Title: f.Heading, Body: f.Body}}
	var a *newsv1.Article
	var err error
	if f.ID == "" {
		a, err = s.News.CreateArticle(r.Context(), &newsv1.CreateArticleRequest{
			Slug: f.Slug, Audience: &newsv1.Audience{Platforms: splitList(f.Platforms)}, Locales: loc})
	} else {
		// An update replaces the whole audience, so the dimensions this form
		// does not show are carried over rather than silently widened to all.
		var cur *newsv1.Article
		if cur, err = s.News.GetArticle(r.Context(), &newsv1.ArticleRef{Id: f.ID}); err == nil {
			aud := cur.GetAudience()
			platforms := splitList(f.Platforms)
			if len(platforms) == 0 {
				platforms = []string{"all"}
			}
			a, err = s.News.UpdateArticle(r.Context(), &newsv1.UpdateArticleRequest{Id: f.ID, Locales: loc,
				Audience: &newsv1.Audience{Platforms: platforms, TitleIds: aud.GetTitleIds(), Regions: aud.GetRegions(), Languages: aud.GetLanguages()}})
		}
	}
	if err != nil {
		f.base, f.Error = s.page(r, ws, i18n.T("en", "admin.news.edit")), grpcMessage(err)
		s.render(w, http.StatusBadRequest, "admin_news_edit.html", f)
		return
	}
	action := "news.update"
	if f.ID == "" {
		action = "news.create"
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, a.GetId()+" "+a.GetSlug())
	http.Redirect(w, r, "/admin/news", http.StatusSeeOther)
}

// handleNewsPublish publishes now, or unpublishes, depending on the path.
func (s *Server) handleNewsPublish(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok {
		return
	}
	if s.News == nil {
		http.Error(w, i18n.T("en", "admin.news.off"), http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	var a *newsv1.Article
	var err error
	action := "news.publish"
	if strings.HasSuffix(r.URL.Path, "/unpublish") {
		action = "news.unpublish"
		a, err = s.News.UnpublishArticle(r.Context(), &newsv1.ArticleRef{Id: id})
	} else {
		a, err = s.News.PublishArticle(r.Context(), &newsv1.PublishArticleRequest{Id: id})
	}
	if err != nil {
		http.Error(w, grpcMessage(err), http.StatusBadRequest)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, a.GetId()+" "+a.GetSlug())
	http.Redirect(w, r, "/admin/news", http.StatusSeeOther)
}
