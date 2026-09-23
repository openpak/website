package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openpak/website/internal/i18n"
	"github.com/openpak/website/internal/store"
)

// Console news (/admin/console-news): the channels and articles of the Switch
// News tab, through the news service's admin API (news docs/admin-api.md,
// NEWS_ADMIN_URL + NEWS_ADMIN_KEY as X-Internal-Key). Edits change the
// source only; nothing reaches a console until Publish, which the service
// builds, signs and verifies itself. Rollback switches what consoles are
// served back to an earlier build.

// ConsoleNewsAPI is the news service's /admin/v1; nil hides the section.
type ConsoleNewsAPI struct{ c *keyClient }

// NewConsoleNewsAPI returns nil when NEWS_ADMIN_URL or NEWS_ADMIN_KEY is unset.
func NewConsoleNewsAPI(baseURL, key string) *ConsoleNewsAPI {
	c := newKeyClient(baseURL, key)
	if c == nil {
		return nil
	}
	return &ConsoleNewsAPI{c: c}
}

type cnMotif struct {
	Motif string `json:"motif"`
	Hue   int    `json:"hue"`
}

type cnChannel struct {
	TopicID            string   `json:"topic_id"`
	Name               string   `json:"name"`
	Publisher          string   `json:"publisher"`
	Description        string   `json:"description"`
	Important          bool     `json:"important"`
	Icon               cnMotif  `json:"icon"`
	HasIconImage       bool     `json:"has_icon_image"`
	ShopQuery          string   `json:"shop_query"`
	ApplicationIDs     []string `json:"application_ids"`
	TestDistribution   bool     `json:"test_distribution"`
	Retired            bool     `json:"retired"`
	RetireReason       string   `json:"retire_reason"`
	ArticleCount       int      `json:"article_count"`
	ActiveArticleCount int      `json:"active_article_count"`
}

type cnEssential struct {
	PickupDays    int `json:"pickup_days"`
	PriorityAfter int `json:"priority_after"`
}

type cnMore struct {
	Type           string   `json:"type"`
	ApplicationIDs []string `json:"application_ids,omitempty"`
	Query          string   `json:"query,omitempty"`
	URL            string   `json:"url,omitempty"`
}

type cnArticle struct {
	TopicID            string       `json:"topic_id"`
	NewsID             uint32       `json:"news_id"`
	Title              string       `json:"title"`
	Body               string       `json:"body"`
	Footer             string       `json:"footer"`
	Language           string       `json:"language"`
	SupportedLanguages []string     `json:"supported_languages"`
	LifetimeDays       int          `json:"lifetime_days"`
	PublishedAt        *string      `json:"published_at"`
	ExpireAt           *string      `json:"expire_at"`
	PickupDays         *int         `json:"pickup_days"`
	Priority           int          `json:"priority"`
	EssentialPickup    *cnEssential `json:"essential_pickup"`
	Art                cnMotif      `json:"art"`
	HasImage           bool         `json:"has_image"`
	More               *cnMore      `json:"more"`
	Retired            bool         `json:"retired"`
	RetireReason       string       `json:"retire_reason"`
	EverPublished      bool         `json:"ever_published"`
}

type cnBuild struct {
	Build         int      `json:"build"`
	At            string   `json:"at"`
	CreatedAt     string   `json:"created_at"`
	Containers    int      `json:"containers"`
	Channels      int      `json:"channels"`
	Articles      int      `json:"articles"`
	ChangedTopics []string `json:"changed_topics"`
	Note          string   `json:"note"`
}

type cnStatus struct {
	Serving struct {
		Build *int   `json:"build"`
		Since string `json:"since"`
	} `json:"serving"`
	Revision          int      `json:"revision"`
	Dirty             bool     `json:"dirty"`
	SigningConfigured bool     `json:"signing_configured"`
	PushConfigured    bool     `json:"push_configured"`
	LastPublish       *cnBuild `json:"last_publish"`
	LastError         *struct {
		At    string `json:"at"`
		Op    string `json:"op"`
		Error string `json:"error"`
	} `json:"last_error"`
	Pushes []struct {
		TopicID  string `json:"topic_id"`
		Revision int    `json:"revision"`
		At       string `json:"at"`
		OK       bool   `json:"ok"`
		Status   int    `json:"status"`
		Error    string `json:"error"`
	} `json:"pushes"`
	Builds []cnBuild `json:"builds"`
}

type cnMotifs struct {
	Icon  []string `json:"icon"`
	Scene []string `json:"scene"`
}

func cnTopic(topic string) string { return "/admin/v1/channels/" + url.PathEscape(topic) }
func cnArticlePath(topic, id string) string {
	return cnTopic(topic) + "/articles/" + url.PathEscape(id)
}

func (a *ConsoleNewsAPI) get(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return a.c.sendJSON(ctx, http.MethodGet, path, nil, out)
}

func (s *Server) cnOff(w http.ResponseWriter, r *http.Request) bool {
	if s.ConsoleNews != nil {
		return false
	}
	http.NotFound(w, r)
	return true
}

func cnFlash(code string) string {
	switch code {
	case "published", "rolledback", "created", "saved", "icon", "image", "retired", "restored", "deleted", "retired-instead":
		return i18n.T("en", "admin.cnews.flash."+code)
	}
	return ""
}

// handleConsoleNews is the dashboard: build status, publish and rollback, and
// the channel lineup.
func (s *Server) handleConsoleNews(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	s.renderConsoleNews(w, r, ws, http.StatusOK, "", nil)
}

func (s *Server) renderConsoleNews(w http.ResponseWriter, r *http.Request, ws store.WebSession, status int, errMsg string, result *cnBuild) {
	v := struct {
		base
		Status       cnStatus
		ServingBuild int // 0 while the start-up fixture is served
		Channels     []cnChannel
		Result       *cnBuild
		Error        string
	}{base: s.page(r, ws, i18n.T("en", "admin.cnews")), Result: result, Error: errMsg}
	v.Path = "/admin/console-news"
	v.Flash = cnFlash(r.URL.Query().Get("m"))
	var errs []string
	if err := s.ConsoleNews.get(r.Context(), "/admin/v1/status", &v.Status); err != nil {
		errs = append(errs, "status: "+err.Error())
	}
	if v.Status.Serving.Build != nil {
		v.ServingBuild = *v.Status.Serving.Build
	}
	var ch struct {
		Channels []cnChannel `json:"channels"`
	}
	if err := s.ConsoleNews.get(r.Context(), "/admin/v1/channels", &ch); err != nil {
		errs = append(errs, "channels: "+err.Error())
	}
	v.Channels = ch.Channels
	if v.Error == "" && len(errs) > 0 {
		v.Error = strings.Join(errs, "; ")
	}
	s.render(w, status, "admin_cnews.html", v)
}

// handleConsoleNewsBuild is Publish or Rollback. The service answers with
// the build it switched to, or why nothing was switched.
func (s *Server) handleConsoleNewsBuild(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	op := r.PathValue("op")
	body := map[string]any{}
	switch op {
	case "publish":
		if note := strings.TrimSpace(r.PostFormValue("note")); note != "" {
			body["note"] = note
		}
	case "rollback":
		if b := strings.TrimSpace(r.PostFormValue("build")); b != "" {
			n, err := strconv.Atoi(b)
			if err != nil {
				s.renderConsoleNews(w, r, ws, http.StatusBadRequest, "build must be a number", nil)
				return
			}
			body["build"] = n
		}
	default:
		http.NotFound(w, r)
		return
	}
	// Signing every container takes a while; give it a minute.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var res cnBuild
	if err := s.ConsoleNews.c.sendJSON(ctx, http.MethodPost, "/admin/v1/"+op, body, &res); err != nil {
		s.renderConsoleNews(w, r, ws, http.StatusBadGateway, op+" failed, nothing was switched: "+err.Error(), nil)
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, "cnews."+op, fmt.Sprintf("build=%d changed=%s note=%q", res.Build, strings.Join(res.ChangedTopics, ","), res.Note))
	s.renderConsoleNews(w, r, ws, http.StatusOK, "", &res)
}

// --- channels ---

type cnChannelForm struct {
	base
	Exists   bool
	Channel  cnChannel
	Articles []cnArticle
	Motifs   cnMotifs
	AppIDs   string
	Error    string
}

func (s *Server) channelForm(r *http.Request, ws store.WebSession, c cnChannel, exists bool) cnChannelForm {
	f := cnChannelForm{base: s.page(r, ws, i18n.T("en", "admin.cnews.channel.new")), Exists: exists, Channel: c,
		AppIDs: strings.Join(c.ApplicationIDs, ", ")}
	if exists {
		f.base = s.page(r, ws, c.Name)
	}
	_ = s.ConsoleNews.get(r.Context(), "/admin/v1/motifs", &f.Motifs)
	return f
}

func (s *Server) handleCNChannelNew(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	s.render(w, http.StatusOK, "admin_cnews_channel.html", s.channelForm(r, ws, cnChannel{TopicID: "openpak_", Icon: cnMotif{Motif: "mark"}}, false))
}

func (s *Server) handleCNChannel(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	s.renderCNChannel(w, r, ws, r.PathValue("topic"), http.StatusOK, "")
}

func (s *Server) renderCNChannel(w http.ResponseWriter, r *http.Request, ws store.WebSession, topic string, status int, errMsg string) {
	var c cnChannel
	if err := s.ConsoleNews.get(r.Context(), cnTopic(topic), &c); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	f := s.channelForm(r, ws, c, true)
	var arts struct {
		Articles []cnArticle `json:"articles"`
	}
	if err := s.ConsoleNews.get(r.Context(), cnTopic(topic)+"/articles", &arts); err != nil && errMsg == "" {
		errMsg = err.Error()
	}
	f.Articles, f.Error = arts.Articles, errMsg
	f.Flash = cnFlash(r.URL.Query().Get("m"))
	s.render(w, status, "admin_cnews_channel.html", f)
}

// handleCNChannelSave creates or updates a channel. Every writable field is
// sent, so what the form shows is what the channel becomes.
func (s *Server) handleCNChannelSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	topic := r.PathValue("topic")
	hue, _ := strconv.Atoi(r.PostFormValue("icon_hue"))
	ids := splitList(strings.ToLower(r.PostFormValue("application_ids")))
	body := map[string]any{
		"name": strings.TrimSpace(r.PostFormValue("name")), "publisher": strings.TrimSpace(r.PostFormValue("publisher")),
		"description": strings.TrimSpace(r.PostFormValue("description")), "important": r.PostFormValue("important") == "1",
		"icon":       cnMotif{Motif: r.PostFormValue("icon_motif"), Hue: hue},
		"shop_query": strings.TrimSpace(r.PostFormValue("shop_query")), "test_distribution": r.PostFormValue("test_distribution") == "1",
		"application_ids": ids,
	}
	if ids == nil {
		body["application_ids"] = nil
	}
	var c cnChannel
	var err error
	action, flash := "cnews.channel.update", "saved"
	if topic == "" {
		topic = strings.TrimSpace(r.PostFormValue("topic_id"))
		body["topic_id"] = topic
		if ids == nil {
			delete(body, "application_ids")
		}
		action, flash = "cnews.channel.create", "created"
		err = s.ConsoleNews.c.sendJSON(r.Context(), http.MethodPost, "/admin/v1/channels", body, &c)
	} else {
		err = s.ConsoleNews.c.sendJSON(r.Context(), http.MethodPatch, cnTopic(topic), body, &c)
	}
	if err != nil {
		if action == "cnews.channel.create" {
			f := s.channelForm(r, ws, cnChannel{TopicID: topic, Name: r.PostFormValue("name"), Publisher: r.PostFormValue("publisher"),
				Description: r.PostFormValue("description"), Icon: cnMotif{Motif: r.PostFormValue("icon_motif"), Hue: hue},
				ShopQuery: r.PostFormValue("shop_query"), Important: r.PostFormValue("important") == "1",
				TestDistribution: r.PostFormValue("test_distribution") == "1"}, false)
			f.AppIDs, f.Error = r.PostFormValue("application_ids"), err.Error()
			s.render(w, http.StatusBadRequest, "admin_cnews_channel.html", f)
			return
		}
		s.renderCNChannel(w, r, ws, topic, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, topic)
	http.Redirect(w, r, "/admin/console-news/channels/"+url.PathEscape(topic)+"?m="+flash, http.StatusSeeOther)
}

// handleCNChannelRetire retires (with a reason) or restores a channel.
func (s *Server) handleCNChannelRetire(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	topic := r.PathValue("topic")
	retire := r.PostFormValue("retired") == "1"
	body := map[string]any{"retired": retire}
	if retire {
		body["retire_reason"] = strings.TrimSpace(r.PostFormValue("reason"))
	}
	if err := s.ConsoleNews.c.sendJSON(r.Context(), http.MethodPatch, cnTopic(topic), body, nil); err != nil {
		s.renderCNChannel(w, r, ws, topic, http.StatusBadRequest, err.Error())
		return
	}
	action, flash := "cnews.channel.retire", "retired"
	if !retire {
		action, flash = "cnews.channel.restore", "restored"
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, fmt.Sprintf("%s reason=%q", topic, r.PostFormValue("reason")))
	http.Redirect(w, r, "/admin/console-news/channels/"+url.PathEscape(topic)+"?m="+flash, http.StatusSeeOther)
}

// imageUpload relays one uploaded PNG/JPEG (16 MiB at most) as the raw PUT
// body the news service takes, streamed from the parsed form's temp file.
func (s *Server) imageUpload(r *http.Request, path string) error {
	f, h, err := r.FormFile("image")
	if err != nil {
		return errors.New(i18n.T("en", "admin.cnews.noimage"))
	}
	defer f.Close()
	ct := h.Header.Get("Content-Type")
	if ct != "image/png" && ct != "image/jpeg" {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		ct = http.DetectContentType(head[:n])
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	return s.ConsoleNews.c.send(r.Context(), http.MethodPut, path, ct, f, nil)
}

// handleCNImage uploads or removes a channel icon or an article image.
func (s *Server) handleCNImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 17<<20)
	ws, ok := s.authorizePost(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	topic, id := r.PathValue("topic"), r.PathValue("id")
	path, back, what := cnTopic(topic)+"/icon", "/admin/console-news/channels/"+url.PathEscape(topic), "icon"
	if id != "" {
		path, back, what = cnArticlePath(topic, id)+"/image", back+"/articles/"+url.PathEscape(id), "image"
	}
	var err error
	remove := r.PostFormValue("remove") == "1"
	if remove {
		err = s.ConsoleNews.c.send(r.Context(), http.MethodDelete, path, "", nil, nil)
	} else {
		err = s.imageUpload(r, path)
	}
	if err != nil {
		if id != "" {
			s.renderCNArticle(w, r, ws, topic, id, http.StatusBadRequest, err.Error())
		} else {
			s.renderCNChannel(w, r, ws, topic, http.StatusBadRequest, err.Error())
		}
		return
	}
	action := "cnews." + what + ".upload"
	if remove {
		action = "cnews." + what + ".remove"
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, strings.TrimSuffix(topic+" "+id, " "))
	http.Redirect(w, r, back+"?m="+what, http.StatusSeeOther)
}

// handleCNImageShow shows the stored icon or image beside its form.
func (s *Server) handleCNImageShow(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorize(w, r); !ok || s.cnOff(w, r) {
		return
	}
	topic, id := r.PathValue("topic"), r.PathValue("id")
	path := cnTopic(topic) + "/icon"
	if id != "" {
		path = cnArticlePath(topic, id) + "/image"
	}
	s.ConsoleNews.c.proxy(w, r, path)
}

// --- articles ---

type cnArticleForm struct {
	base
	Exists  bool
	Topic   string
	Article cnArticle
	Motifs  cnMotifs
	// Form values as typed.
	NewsID, Langs, PublishedAt, PickupDays, EssentialDays, EssentialAfter string
	MoreType, MoreIDs, MoreQuery, MoreURL                                 string
	Error                                                                 string
}

func (s *Server) articleForm(r *http.Request, ws store.WebSession, topic string, a cnArticle, exists bool) cnArticleForm {
	f := cnArticleForm{base: s.page(r, ws, i18n.T("en", "admin.cnews.article.new")), Exists: exists, Topic: topic, Article: a,
		Langs: strings.Join(a.SupportedLanguages, ", ")}
	if exists {
		f.base = s.page(r, ws, a.Title)
		f.NewsID = strconv.FormatUint(uint64(a.NewsID), 10)
	}
	if a.PublishedAt != nil {
		if t, err := time.Parse(time.RFC3339, *a.PublishedAt); err == nil {
			f.PublishedAt = t.UTC().Format("2006-01-02T15:04")
		}
	}
	if a.PickupDays != nil {
		f.PickupDays = strconv.Itoa(*a.PickupDays)
	}
	if a.EssentialPickup != nil {
		f.EssentialDays, f.EssentialAfter = strconv.Itoa(a.EssentialPickup.PickupDays), strconv.Itoa(a.EssentialPickup.PriorityAfter)
	}
	if a.More != nil {
		f.MoreType, f.MoreIDs, f.MoreQuery, f.MoreURL = a.More.Type, strings.Join(a.More.ApplicationIDs, ", "), a.More.Query, a.More.URL
	}
	_ = s.ConsoleNews.get(r.Context(), "/admin/v1/motifs", &f.Motifs)
	return f
}

func (s *Server) handleCNArticleNew(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	s.render(w, http.StatusOK, "admin_cnews_article.html", s.articleForm(r, ws, r.PathValue("topic"),
		cnArticle{Language: "en-GB", SupportedLanguages: []string{"en-GB", "en-US"}, LifetimeDays: 14, Priority: 100, Art: cnMotif{Motif: "burst"}}, false))
}

func (s *Server) handleCNArticle(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorize(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	s.renderCNArticle(w, r, ws, r.PathValue("topic"), r.PathValue("id"), http.StatusOK, "")
}

func (s *Server) renderCNArticle(w http.ResponseWriter, r *http.Request, ws store.WebSession, topic, id string, status int, errMsg string) {
	var a cnArticle
	if err := s.ConsoleNews.get(r.Context(), cnArticlePath(topic, id), &a); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	f := s.articleForm(r, ws, topic, a, true)
	f.Error = errMsg
	f.Flash = cnFlash(r.URL.Query().Get("m"))
	s.render(w, status, "admin_cnews_article.html", f)
}

// orNull is a JSON null for an empty number, not a typed nil pointer.
func orNull(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// articleBody turns the form into the API's article fields. Empty optional
// fields are null, which the API reads as "reset to the default".
func articleBody(r *http.Request) (map[string]any, error) {
	num := func(name string) (*int, error) {
		v := strings.TrimSpace(r.PostFormValue(name))
		if v == "" {
			return nil, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("%s must be a whole number", strings.ReplaceAll(name, "_", " "))
		}
		return &n, nil
	}
	lifetime, err := num("lifetime_days")
	if err != nil {
		return nil, err
	}
	priority, err := num("priority")
	if err != nil {
		return nil, err
	}
	pickup, err := num("pickup_days")
	if err != nil {
		return nil, err
	}
	essDays, err := num("essential_days")
	if err != nil {
		return nil, err
	}
	essAfter, err := num("essential_after")
	if err != nil {
		return nil, err
	}
	hue, _ := strconv.Atoi(r.PostFormValue("art_hue"))
	// One paragraph: the native reader's handling of newlines is unverified,
	// so the form's line breaks become spaces rather than a refusal.
	body := strings.Join(strings.Fields(strings.ReplaceAll(r.PostFormValue("body"), "\r", " ")), " ")
	out := map[string]any{
		"title": strings.TrimSpace(r.PostFormValue("title")), "body": body, "footer": strings.TrimSpace(r.PostFormValue("footer")),
		"language": strings.TrimSpace(r.PostFormValue("language")), "supported_languages": splitList(r.PostFormValue("supported_languages")),
		"lifetime_days": orNull(lifetime), "priority": orNull(priority), "pickup_days": orNull(pickup),
		"art": cnMotif{Motif: r.PostFormValue("art_motif"), Hue: hue}, "essential_pickup": nil, "more": nil, "published_at": nil,
	}
	if priority == nil {
		out["priority"] = 100
	}
	if essDays != nil {
		e := map[string]int{"pickup_days": *essDays}
		if essAfter != nil {
			e["priority_after"] = *essAfter
		}
		out["essential_pickup"] = e
	}
	if v := strings.TrimSpace(r.PostFormValue("published_at")); v != "" {
		t, err := time.Parse("2006-01-02T15:04", v)
		if err != nil {
			return nil, errors.New("published at: a date and time (UTC)")
		}
		out["published_at"] = t.UTC().Format(time.RFC3339)
	}
	ids := splitList(strings.ToLower(r.PostFormValue("more_ids")))
	switch r.PostFormValue("more_type") {
	case "game":
		out["more"] = cnMore{Type: "game", ApplicationIDs: ids}
	case "shop":
		out["more"] = cnMore{Type: "shop", Query: strings.TrimSpace(r.PostFormValue("more_query")), ApplicationIDs: ids}
	case "browser":
		out["more"] = cnMore{Type: "browser", URL: strings.TrimSpace(r.PostFormValue("more_url"))}
	}
	return out, nil
}

func (s *Server) handleCNArticleSave(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	topic, id := r.PathValue("topic"), r.PathValue("id")
	fail := func(msg string) {
		if id != "" {
			s.renderCNArticle(w, r, ws, topic, id, http.StatusBadRequest, msg)
			return
		}
		f := s.articleForm(r, ws, topic, cnArticle{Title: r.PostFormValue("title"), Body: r.PostFormValue("body"),
			Footer: r.PostFormValue("footer"), Language: r.PostFormValue("language"), Art: cnMotif{Motif: r.PostFormValue("art_motif")}}, false)
		f.Article.LifetimeDays, _ = strconv.Atoi(r.PostFormValue("lifetime_days"))
		f.Article.Priority, _ = strconv.Atoi(r.PostFormValue("priority"))
		f.NewsID, f.Langs, f.PublishedAt, f.PickupDays = r.PostFormValue("news_id"), r.PostFormValue("supported_languages"), r.PostFormValue("published_at"), r.PostFormValue("pickup_days")
		f.EssentialDays, f.EssentialAfter = r.PostFormValue("essential_days"), r.PostFormValue("essential_after")
		f.MoreType, f.MoreIDs, f.MoreQuery, f.MoreURL = r.PostFormValue("more_type"), r.PostFormValue("more_ids"), r.PostFormValue("more_query"), r.PostFormValue("more_url")
		f.Error = msg
		s.render(w, http.StatusBadRequest, "admin_cnews_article.html", f)
	}
	body, err := articleBody(r)
	if err != nil {
		fail(err.Error())
		return
	}
	var a cnArticle
	action := "cnews.article.update"
	if id == "" {
		action = "cnews.article.create"
		if v := strings.TrimSpace(r.PostFormValue("news_id")); v != "" {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				fail("news id: a number from 1 to 4294967295, or empty")
				return
			}
			body["news_id"] = n
		}
		for _, k := range []string{"essential_pickup", "more", "published_at", "pickup_days"} {
			if body[k] == nil {
				delete(body, k)
			}
		}
		err = s.ConsoleNews.c.sendJSON(r.Context(), http.MethodPost, cnTopic(topic)+"/articles", body, &a)
	} else {
		err = s.ConsoleNews.c.sendJSON(r.Context(), http.MethodPatch, cnArticlePath(topic, id), body, &a)
	}
	if err != nil {
		fail(err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, fmt.Sprintf("%s %d", topic, a.NewsID))
	http.Redirect(w, r, fmt.Sprintf("/admin/console-news/channels/%s/articles/%d?m=saved", url.PathEscape(topic), a.NewsID), http.StatusSeeOther)
}

// handleCNArticleState retires, restores or deletes an article. Deleting one
// that ever shipped retires it instead; the service says which happened.
func (s *Server) handleCNArticleState(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.authorizePost(w, r)
	if !ok || s.cnOff(w, r) {
		return
	}
	topic, id := r.PathValue("topic"), r.PathValue("id")
	back := "/admin/console-news/channels/" + url.PathEscape(topic)
	var err error
	var action, flash string
	switch r.PathValue("op") {
	case "retire":
		retire := r.PostFormValue("retired") == "1"
		body := map[string]any{"retired": retire}
		action, flash = "cnews.article.restore", "restored"
		if retire {
			body["retire_reason"] = strings.TrimSpace(r.PostFormValue("reason"))
			action, flash = "cnews.article.retire", "retired"
		}
		back += "/articles/" + url.PathEscape(id)
		err = s.ConsoleNews.c.sendJSON(r.Context(), http.MethodPatch, cnArticlePath(topic, id), body, nil)
	case "delete":
		var res struct {
			Deleted bool `json:"deleted"`
			Retired bool `json:"retired"`
		}
		err = s.ConsoleNews.c.sendJSON(r.Context(), http.MethodDelete, cnArticlePath(topic, id), nil, &res)
		action, flash = "cnews.article.delete", "deleted"
		if res.Retired {
			flash = "retired-instead"
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.renderCNArticle(w, r, ws, topic, id, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.DB.AddAudit(r.Context(), ws.AccountID, action, fmt.Sprintf("%s %s reason=%q", topic, id, r.PostFormValue("reason")))
	http.Redirect(w, r, back+"?m="+flash, http.StatusSeeOther)
}
