package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const rawSecret = "postgres://user:sup3rs3cr3t@db:5432/app"

// TestSecretRedactsThroughEveryRendering asserts the raw value reaches none of
// the three surfaces a configuration value normally leaks through: a formatted
// string, a marshalled document, and a log record.
func TestSecretRedactsThroughEveryRendering(t *testing.T) {
	s := Secret(rawSecret)

	holder := struct {
		DatabaseURL Secret `json:"database_url"`
		Name        string `json:"name"`
	}{DatabaseURL: s, Name: "service"}

	encoded, err := json.Marshal(holder)
	if err != nil {
		t.Fatalf("marshal the holder: %v", err)
	}

	var logged bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	logger.Info("configuration", "database_url", s, "holder", fmt.Sprint(holder))

	renderings := map[string]string{
		"%v":             fmt.Sprintf("%v", s),
		"%s":             fmt.Sprintf("value=%s", s),
		"%q":             fmt.Sprintf("%q", s),
		"%#v":            fmt.Sprintf("%#v", s),
		"%v of a struct": fmt.Sprintf("%v", holder),
		"String":         s.String(),
		"json":           string(encoded),
		"slog":           logged.String(),
	}
	for name, got := range renderings {
		if strings.Contains(got, rawSecret) {
			t.Errorf("%s rendered the raw secret: %s", name, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Errorf("%s did not render the placeholder: %s", name, got)
		}
	}
}

// TestSecretRedactionFailsWhenTheTypeIsBypassed proves the assertions above
// are not vacuous: an ordinary string carrying the same value does leak, so the
// redaction comes from the type and not from the test.
func TestSecretRedactionFailsWhenTheTypeIsBypassed(t *testing.T) {
	plain := string(Secret(rawSecret))
	if !strings.Contains(fmt.Sprintf("%v", plain), rawSecret) {
		t.Fatal("a converted secret did not leak, so the leak assertions prove nothing")
	}
	encoded, err := json.Marshal(map[string]string{"database_url": plain})
	if err != nil {
		t.Fatalf("marshal the plain value: %v", err)
	}
	if !strings.Contains(string(encoded), rawSecret) {
		t.Fatal("a converted secret did not leak through json")
	}
}

func TestSecretRevealReturnsTheValue(t *testing.T) {
	s := Secret(rawSecret)
	if got := s.Reveal(); got != rawSecret {
		t.Fatalf("Reveal = %q, want the raw value", got)
	}
	if !Secret("").IsZero() {
		t.Fatal("an empty secret reported itself as set")
	}
	if s.IsZero() {
		t.Fatal("a populated secret reported itself as unset")
	}
}

func TestSecretMarshalTextRedacts(t *testing.T) {
	text, err := Secret(rawSecret).MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(text) != Redacted {
		t.Fatalf("MarshalText = %q, want %q", text, Redacted)
	}
}

func TestSecretUnmarshalTextAcceptsTheRawValue(t *testing.T) {
	var s Secret
	if err := s.UnmarshalText([]byte(rawSecret)); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if s.Reveal() != rawSecret {
		t.Fatalf("Reveal = %q, want the raw value", s.Reveal())
	}
}

func TestSecretLogValueIsAString(t *testing.T) {
	if got := Secret(rawSecret).LogValue(); got.Kind() != slog.KindString || got.String() != Redacted {
		t.Fatalf("LogValue = %v, want the placeholder string", got)
	}
}
