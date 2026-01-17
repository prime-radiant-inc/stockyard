package dashboard

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

// LoadTemplates loads all HTML templates.
func LoadTemplates() (*template.Template, error) {
	return template.ParseFS(templateFS, "templates/*.html")
}
