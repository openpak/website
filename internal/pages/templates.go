package pages

import (
	"embed"
	"html/template"
	"strings"

	"github.com/openpak/website/internal/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

// LoadTemplates parses the embedded page templates with the i18n function
// bound as `t` (WEB-09: one string catalogue).
func LoadTemplates() (*template.Template, error) {
	return template.New("site").Funcs(template.FuncMap{"t": i18n.T, "langs": i18n.Supported, "date": i18n.Date, "consoleSlug": consoleSlug, "consoleSlugs": consoleSlugs, "titleArt": artPath, "badgeClass": badgeClass, "hasPrefix": strings.HasPrefix, "lower": strings.ToLower, "arr": func(v ...any) []any { return v }}).ParseFS(templateFS, "templates/*.html")
}
