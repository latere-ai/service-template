package main

import (
	"bytes"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"example.com/service/internal/httpx"
	"example.com/service/internal/server"
)

// Route is one entry of the interface reference. The path, the method, and the
// access rule come from the route table the service mounts; the summary is the
// one sentence a client developer needs to decide whether to call it.
type Route struct {
	Method  string
	Path    string
	Access  string
	Summary string
}

// operationalRoutes are the endpoints the runtime serves for an orchestrator
// and a release pipeline. Their paths are read from the runtime package, so a
// changed path changes this document.
func operationalRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: server.LivePath, Access: "public",
			Summary: "Reports that the process is running. It answers while the process is draining.",
		},
		{
			Method: http.MethodGet, Path: server.ReadyPath, Access: "public",
			Summary: "Reports that every registered dependency is reachable. " +
				"It answers 503 while the process is draining or while a dependency is down, " +
				"and names the failing dependency in the body.",
		},
		{
			Method: http.MethodGet, Path: server.VersionPath, Access: "public",
			Summary: "Reports the build identity of the running binary: version, commit, build time, and asset hash.",
		},
	}
}

// applicationRoutes are the endpoints the service adds. The baseline serves
// none, so a generated repository starts with an interface reference that is
// accurate rather than with an example that is not served.
func applicationRoutes() []Route { return nil }

// envelopeFields describes the members of the error envelope, keyed by the
// type and the JSON member. A member with no entry here fails generation, so a
// field added to the envelope reaches this document instead of reaching a
// client undocumented.
var envelopeFields = map[string]string{
	"Problem.type":     "URI that identifies the error class. It is stable, and a client branches on it.",
	"Problem.title":    "Short, stable summary of the error class.",
	"Problem.status":   "HTTP status code, repeated in the body so a logged body is self-contained.",
	"Problem.detail":   "Explanation of this occurrence. It is safe to show to a user and is never an internal message.",
	"Problem.instance": "Request identifier of the failed request. Quote it in a bug report; it joins the response to the server logs and to the trace.",
	"Problem.errors":   "Rejected input fields, present on a validation failure.",

	"FieldError.field":  "Name of the rejected input field, in the same spelling the request used.",
	"FieldError.code":   "Stable machine token for the reason, such as `required` or `format`.",
	"FieldError.detail": "What is wrong with the value. It is safe to show to a user.",
}

// apiIntro is the prose around the generated tables. It addresses one
// audience, a developer writing a client, and states the three rules a client
// depends on: where the endpoints live, what an error looks like, and what may
// change without notice.
const apiIntro = `# Interface reference

The service serves JSON over HTTP. This document lists the endpoints, the shape
of an error, and the rule that decides what may change under a running client.
`

// RenderAPI produces the interface reference from the route table, the error
// envelope, and the version helpers.
func RenderAPI() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(apiIntro)
	b.WriteString("\n")
	b.WriteString(generatedNotice)

	prefix := httpx.Prefix(httpx.CurrentMajor)
	fmt.Fprintf(&b, "\n## Versioning\n\n"+
		"Every application endpoint sits under `%s`. Inside one major version only additive\n"+
		"change is allowed: a new endpoint, a new optional request field, a new response field.\n"+
		"Removing a field, narrowing a type, or making an optional field required is a new\n"+
		"major version, served beside the old one at its own prefix.\n\n"+
		"A client ignores response fields it does not know, because new ones appear without a\n"+
		"version change.\n", prefix)

	b.WriteString("\nAn endpoint on its way out answers with these headers before it stops answering:\n\n")
	b.WriteString("| Header | Meaning |\n| --- | --- |\n")
	b.WriteString("| `Deprecation` | When the endpoint was announced as deprecated. |\n")
	b.WriteString("| `Sunset` | When the endpoint stops answering. |\n")
	b.WriteString("| `Link` with `rel=\"successor-version\"` | The endpoint that replaces it. |\n")
	b.WriteString("| `Link` with `rel=\"deprecation\"` | The document describing the change. |\n")

	writeRoutes(&b, "Application endpoints", applicationRoutes(),
		"The baseline serves no application endpoint. An endpoint appears here when the\n"+
			"service registers it in its route table.")
	writeRoutes(&b, "Operational endpoints", operationalRoutes(), "")

	fmt.Fprintf(&b, "\n## Request identification\n\n"+
		"Send `%s` to correlate a request with your own logs. The service accepts the value\n"+
		"when it is present, generates one when it is not, and returns it on every response,\n"+
		"including every error.\n", httpx.HeaderRequestID)

	if err := writeEnvelope(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeRoutes renders one endpoint table, or the note that stands in for an
// empty one.
func writeRoutes(b *bytes.Buffer, title string, routes []Route, empty string) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	if len(routes) == 0 {
		fmt.Fprintf(b, "%s\n", empty)
		return
	}
	b.WriteString("| Method | Path | Access | Purpose |\n| --- | --- | --- | --- |\n")
	for _, r := range routes {
		fmt.Fprintf(b, "| %s | `%s` | %s | %s |\n", r.Method, r.Path, cell(r.Access), cell(r.Summary))
	}
}

// writeEnvelope renders the error body from the type the writer serializes, so
// a field added to the envelope appears here or fails the build.
func writeEnvelope(b *bytes.Buffer) error {
	fmt.Fprintf(b, "\n## Errors\n\n"+
		"Every failure answers with the same body, served as `%s`. One shape means a client\n"+
		"parses one thing, whichever endpoint failed.\n\n",
		httpx.ProblemContentType)

	fmt.Fprintf(b, "```json\n"+
		"{\n"+
		"  \"type\": %q,\n"+
		"  \"title\": \"Bad Request\",\n"+
		"  \"status\": 400,\n"+
		"  \"detail\": \"body is not valid JSON\",\n"+
		"  \"instance\": \"req_01J0000000000000000000\"\n"+
		"}\n```\n", httpx.TypeBase+"bad-request")

	for _, section := range []struct {
		title string
		intro string
		typ   reflect.Type
	}{
		{"Error members", "", reflect.TypeFor[httpx.Problem]()},
		{"Field errors", "Each entry of `errors` names one rejected input field.",
			reflect.TypeFor[httpx.FieldError]()},
	} {
		fmt.Fprintf(b, "\n### %s\n\n", section.title)
		if section.intro != "" {
			fmt.Fprintf(b, "%s\n\n", section.intro)
		}
		b.WriteString("| Member | Type | Presence | Meaning |\n| --- | --- | --- | --- |\n")
		if err := writeMembers(b, section.typ); err != nil {
			return err
		}
	}

	fmt.Fprintf(b, "\n### Status codes\n\n"+
		"A 4xx means the request must change before it is retried. A 5xx means the request may\n"+
		"be retried. The service also answers `%d` when the client disconnects before the\n"+
		"response is produced, which is neither a fault of the service nor a request it refused.\n",
		httpx.StatusClientClosedRequest)
	return nil
}

// writeMembers renders the JSON members of a struct in declaration order.
func writeMembers(b *bytes.Buffer, t reflect.Type) error {
	for f := range t.Fields() {
		tag, ok := f.Tag.Lookup("json")
		if !ok || !f.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		key := t.Name() + "." + name
		doc, ok := envelopeFields[key]
		if !ok {
			return fmt.Errorf("the error envelope member %s has no description; add one to envelopeFields", key)
		}
		presence := "always"
		if strings.Contains(options, "omitempty") {
			presence = "when it applies"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", name, jsonType(f.Type), presence, cell(doc))
	}
	return nil
}

// jsonType renders a Go type as the JSON type a client parses.
func jsonType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array of " + jsonType(t.Elem()) + "s"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Pointer:
		return jsonType(t.Elem())
	default:
		return t.String()
	}
}
