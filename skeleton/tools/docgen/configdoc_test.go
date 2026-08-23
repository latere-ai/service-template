package main

import (
	"strings"
	"testing"

	"example.com/service/internal/config"
)

func TestConfigurationReferenceListsEveryDeclaredField(t *testing.T) {
	settings, err := settingsOf(&config.Config{})
	if err != nil {
		t.Fatalf("read the configuration struct: %v", err)
	}
	doc, err := RenderConfiguration()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(doc)
	for _, s := range settings {
		if !strings.Contains(text, "`"+s.Env+"`") {
			t.Errorf("the reference does not list %s", s.Env)
		}
		if s.Doc != "" && !strings.Contains(text, s.Doc) {
			t.Errorf("the reference does not carry the explanation of %s", s.Env)
		}
	}
}

// The reference and the example environment file are two renderings of one
// declaration. A variable in one and not in the other means an operator reads
// two different sets.
func TestReferenceAndExampleFileDescribeTheSameVariables(t *testing.T) {
	example, err := config.EnvExample()
	if err != nil {
		t.Fatalf("render the example file: %v", err)
	}
	settings, err := settingsOf(&config.Config{})
	if err != nil {
		t.Fatalf("read the configuration struct: %v", err)
	}
	for _, s := range settings {
		if !strings.Contains(string(example), "\n"+s.Env+"=") {
			t.Errorf("the example file does not declare %s", s.Env)
		}
	}
}

// A renamed field changes the document, which is the whole reason it is
// derived rather than written.
func TestARenamedFieldChangesTheDocument(t *testing.T) {
	type renamed struct {
		Addr string `env:"LISTEN_ADDRESS" default:":8080" doc:"HTTP listen address."`
	}
	settings, err := settingsOf(&renamed{})
	if err != nil {
		t.Fatalf("read the struct: %v", err)
	}
	if len(settings) != 1 || settings[0].Env != "LISTEN_ADDRESS" || settings[0].Flag != "listen-address" {
		t.Fatalf("the rename did not reach the declaration: %+v", settings)
	}
}

func TestSettingsCarryTypeDefaultRequiredAndSecret(t *testing.T) {
	type sample struct {
		Name    string        `env:"NAME" default:"service" doc:"A name."`
		Token   config.Secret `env:"TOKEN" required:"true" doc:"A credential."`
		Timeout string        `env:"TIMEOUT" flag:"-" doc:"A deadline."`
	}
	settings, err := settingsOf(&sample{})
	if err != nil {
		t.Fatalf("read the struct: %v", err)
	}
	if len(settings) != 3 {
		t.Fatalf("want three settings, got %d", len(settings))
	}
	if settings[1].Secret != true || settings[1].Required != true || settings[1].Type != "secret" {
		t.Errorf("the secret field is not reported as a required secret: %+v", settings[1])
	}
	if settings[2].Flag != "" {
		t.Errorf("a field that declines a flag reports one: %+v", settings[2])
	}
}

func TestAStructWithNoDeclaredFieldFails(t *testing.T) {
	type empty struct{ Name string }
	if _, err := settingsOf(&empty{}); err == nil {
		t.Fatal("a struct with no env tag was accepted")
	}
	if _, err := settingsOf(struct{}{}); err == nil {
		t.Fatal("a value that is not a pointer to a struct was accepted")
	}
}
