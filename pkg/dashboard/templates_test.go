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

func TestTemplates_FleetWithTasks(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("failed to load templates: %v", err)
	}

	tasks := []map[string]interface{}{
		{"ID": "vm-1", "Name": "test", "Status": "running"},
		{"ID": "vm-2", "Name": "test2", "Status": "stopped"},
	}

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Title":     "Fleet",
		"User":      "jesse",
		"ActiveNav": "fleet",
		"Tasks":     tasks,
		"GroupedByOwner": map[string][]map[string]interface{}{
			"(unknown)": tasks,
		},
	}

	err = tmpl.ExecuteTemplate(&buf, "fleet.html", data)
	if err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "vm-1") {
		t.Error("expected vm-1 in output")
	}
}

func TestTemplates_VMDetail(t *testing.T) {
	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("failed to load templates: %v", err)
	}

	var buf bytes.Buffer
	data := map[string]interface{}{
		"Title":     "vm-123",
		"User":      "jesse",
		"ActiveNav": "fleet",
		"Task": map[string]interface{}{
			"ID":            "vm-123",
			"Name":          "test-vm",
			"Status":        "running",
			"TailscaleHost": "vm-123.tail.net",
		},
		"Snapshots": []map[string]interface{}{
			{"Name": "snap-1", "Label": "before refactor"},
		},
	}

	err = tmpl.ExecuteTemplate(&buf, "vm_detail.html", data)
	if err != nil {
		t.Fatalf("failed to execute template: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "vm-123") {
		t.Error("expected VM ID in output")
	}
	if !strings.Contains(html, "Copy SSH") {
		t.Error("expected Copy SSH button")
	}
}
