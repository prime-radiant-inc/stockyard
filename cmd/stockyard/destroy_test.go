package main

import (
	"os/exec"
	"strconv"
	"testing"
	"unicode"
)

func TestQuoteTaskNameForDisplayIsTerminalSafeAndReversible(t *testing.T) {
	inputs := []string{
		"production",
		"release candidate",
		"line\nbreak",
		"\x1b[31mred",
		"rtl\u202etext",
		"nul\x00name",
		"café",
	}

	for _, input := range inputs {
		got := quoteTaskNameForDisplay(input)
		for _, r := range got {
			if !unicode.IsPrint(r) {
				t.Fatalf("display %q contains non-printing rune %U", got, r)
			}
		}
		decoded, err := strconv.Unquote(got)
		if err != nil {
			t.Fatalf("unquote display %q: %v", got, err)
		}
		if decoded != input {
			t.Fatalf("display round trip = %q, want %q", decoded, input)
		}
	}
}

func TestQuotePOSIXShellArgumentRoundTripsOneArgument(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "space", value: "release candidate"},
		{name: "single quote", value: "it's ready"},
		{name: "command substitution", value: "$(printf injected)"},
		{name: "backticks", value: "`printf injected`"},
		{name: "leading dash", value: "-production"},
		{name: "unicode", value: "café"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quoted, ok := quotePOSIXShellArgument(tt.value)
			if !ok {
				t.Fatalf("quotePOSIXShellArgument(%q) rejected printable input", tt.value)
			}
			script := "set -- " + quoted + "\nprintf '%s\\n' \"$#\"\nprintf '%s' \"$1\""
			output, err := exec.Command("sh", "-c", script).Output()
			if err != nil {
				t.Fatalf("shell round trip: %v", err)
			}
			want := "1\n" + tt.value
			if string(output) != want {
				t.Fatalf("shell output = %q, want %q", output, want)
			}
		})
	}
}

func TestQuotePOSIXShellArgumentRejectsUnsafeDisplayValues(t *testing.T) {
	values := []string{
		"line\nbreak",
		"tab\tvalue",
		"\x1b[31mred",
		"rtl\u202etext",
		"nul\x00name",
		string([]byte{0xff}),
	}

	for _, value := range values {
		if quoted, ok := quotePOSIXShellArgument(value); ok {
			t.Fatalf("quotePOSIXShellArgument(%q) = %q, want refusal", value, quoted)
		}
	}
}
