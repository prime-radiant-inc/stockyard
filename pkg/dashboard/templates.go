package dashboard

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/js/*.js static/css/*.css
var staticFS embed.FS

// LoadTemplates loads all HTML templates.
func LoadTemplates() (*template.Template, error) {
	return template.ParseFS(templateFS, "templates/*.html")
}

// StaticFileHandler returns an http.Handler that serves static files.
func StaticFileHandler() http.Handler {
	// Get the "static" subdirectory from the embed.FS
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// This should never happen with valid embed directives
		panic("failed to get static subdirectory: " + err.Error())
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
