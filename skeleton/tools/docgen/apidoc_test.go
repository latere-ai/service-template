package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"example.com/service/internal/httpx"
	"example.com/service/internal/server"
)

func TestInterfaceReferenceCarriesTheRoutesAndTheVersionPrefix(t *testing.T) {
	doc, err := RenderAPI()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(doc)
	for _, want := range []string{
		server.LivePath, server.ReadyPath, server.VersionPath,
		httpx.Prefix(httpx.CurrentMajor),
		httpx.HeaderRequestID,
		httpx.ProblemContentType,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the reference does not carry %q", want)
		}
	}
}

// The envelope table is read from the type the writer serializes, so a member
// added to the envelope appears without anyone editing the document.
func TestEnvelopeMembersComeFromTheType(t *testing.T) {
	doc, err := RenderAPI()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	problem := reflect.TypeFor[httpx.Problem]()
	for field := range problem.Fields() {
		tag, ok := field.Tag.Lookup("json")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if !strings.Contains(string(doc), "| `"+name+"` |") {
			t.Errorf("the reference does not describe the member %q", name)
		}
	}
}

// A member with no description fails generation. Without this the document
// would ship a blank cell and a client would be told nothing about a field it
// receives.
func TestAnUndescribedMemberFailsGeneration(t *testing.T) {
	type Undocumented struct {
		Surprise string `json:"surprise"`
	}
	var b bytes.Buffer
	err := writeMembers(&b, reflect.TypeFor[Undocumented]())
	if err == nil {
		t.Fatal("a member with no description was rendered")
	}
	if !strings.Contains(err.Error(), "Undocumented.surprise") {
		t.Errorf("the failure does not name the member: %v", err)
	}
}

func TestPresenceFollowsTheJSONTag(t *testing.T) {
	doc, err := RenderAPI()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(doc)
	if !strings.Contains(text, "| `type` | string | always |") {
		t.Error("a member without omitempty is not reported as always present")
	}
	if !strings.Contains(text, "| `detail` | string | when it applies |") {
		t.Error("a member with omitempty is not reported as conditional")
	}
}
