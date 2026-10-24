package pages

import (
	"embed"
	"html/template"
	"strings"

	"github.com/openpak/website/internal/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

// ContactEmail is the project's public contact address (footer, help, legal pages).
const ContactEmail = "contact@openpak.org"

// CommunityURL is the community invite link (Discord) shown in the footer and on
// /help. Empty draws nothing; set it here once an invite exists.
const CommunityURL = "https://discord.gg/nMhjnsB6F6"

// LoadTemplates parses the embedded page templates with the i18n function
// bound as `t` (WEB-09: one string catalogue).
func LoadTemplates() (*template.Template, error) {
	return template.New("site").Funcs(template.FuncMap{"t": i18n.T, "langs": i18n.Supported, "date": i18n.Date, "consoleSlug": consoleSlug, "consoleSlugs": consoleSlugs, "titleArt": artPath, "badgeClass": badgeClass, "statusText": statusText, "hasPrefix": strings.HasPrefix, "lower": strings.ToLower, "arr": func(v ...any) []any { return v },
		"contactEmail": func() string { return ContactEmail }, "communityURL": func() string { return CommunityURL }}).ParseFS(templateFS, "templates/*.html")
}
