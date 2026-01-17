package dashboard

import (
	"bytes"
	"strings"
	"testing"
)

func TestTemplates_RenderBase(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("failed to load templates: %v", err)
	}

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Title":   "Test Page",
		"Content": "Hello World",
	}

	err = tmpl.ExecuteTemplate(&buf, "base.html", data)
	if err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "Test Page") {
		t.Error("expected title in output")
	}
	if !strings.Contains(html, "htmx") {
		t.Error("expected htmx script in output")
	}
}

func TestTemplates_RenderLayout(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("failed to load templates: %v", err)
	}

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Title":       "Fleet",
		"User":        "jesse",
		"PageContent": "<p>Test content</p>",
	}

	err = tmpl.ExecuteTemplate(&buf, "layout.html", data)
	if err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "Stockyard") {
		t.Error("expected Stockyard branding")
	}
	if !strings.Contains(html, "Fleet") {
		t.Error("expected Fleet nav item")
	}
	if !strings.Contains(html, "jesse") {
		t.Error("expected username in output")
	}
}
