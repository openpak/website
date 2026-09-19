package pages

import (
	"embed"
	"html/template"

	"github.com/openpak/website/internal/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

// LoadTemplates parses the embedded page templates with the i18n function
// bound as `t` (WEB-09: one string catalogue).
func LoadTemplates() (*template.Template, error) {
	return template.New("site").Funcs(template.FuncMap{"t": i18n.T, "langs": i18n.Supported, "consoleSlug": consoleSlug, "badgeClass": badgeClass}).ParseFS(templateFS, "templates/*.html")
}
