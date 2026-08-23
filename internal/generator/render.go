package generator

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"path"
	"strings"
	"text/template"
)

// The generation rewrite is deliberately narrow. Two anchored literals are
// substituted in file content and one path segment is renamed. A bare word
// substitution of "service" would also hit "services", "microservice", and
// http.Server, so anything else that needs the consumer's name belongs in a
// .tmpl file that reads it from the template data.
const (
	// SkeletonModule is the module path the skeleton compiles under.
	SkeletonModule = "example.com/service"
	// SkeletonName is the service name the skeleton uses, including the
	// cmd/service directory.
	SkeletonName = "service"
	// skeletonCmdDir is the command directory renamed to the consumer's name.
	skeletonCmdDir = "cmd/" + SkeletonName
)

// Data is what a .tmpl skeleton file sees. Every field is derived from the
// declaration, so rendering is a pure function of the declaration.
type Data struct {
	// Template is the template identity, for example
	// github.com/latere-ai/service-template.
	Template string
	// Version is the template release the files came from.
	Version string
	// Module is the consumer's Go module path.
	Module string
	// Name is the service name, lower case with hyphens. It is also the
	// cmd/<name> directory.
	Name string
	// Profile is service, library, or frontend-only.
	Profile string
	// Features maps a flag name to whether it is on, for example
	// {{ if .Features.database }}.
	Features map[string]bool
	// Coverage is the statement coverage gate.
	Coverage Coverage
}

// NewData builds the render input for a declaration.
func NewData(cfg *Config) Data {
	features := make(map[string]bool, len(AllFeatures))
	for _, f := range AllFeatures {
		features[f] = cfg.Features[f]
	}
	exclude := append([]string(nil), cfg.Coverage.Exclude...)
	return Data{
		Template: cfg.Template,
		Version:  cfg.Version,
		Module:   cfg.Module,
		Name:     cfg.Name,
		Profile:  cfg.Profile,
		Features: features,
		Coverage: Coverage{Threshold: cfg.Coverage.Threshold, Exclude: exclude},
	}
}

// TargetPath maps a skeleton-form path to its path in the generated
// repository. Only the cmd/service directory is renamed.
func TargetPath(p, name string) string {
	if p == skeletonCmdDir {
		return "cmd/" + name
	}
	if strings.HasPrefix(p, skeletonCmdDir+"/") {
		return "cmd/" + name + strings.TrimPrefix(p, skeletonCmdDir)
	}
	return p
}

// substitute applies the two anchored content literals. Binary content is left
// alone: a byte sequence that happens to match a literal inside an image is
// not a module path.
func substitute(content []byte, cfg *Config) []byte {
	if bytes.IndexByte(content, 0) >= 0 {
		return content
	}
	out := bytes.ReplaceAll(content, []byte(SkeletonModule), []byte(cfg.Module))
	out = bytes.ReplaceAll(out, []byte(skeletonCmdDir), []byte("cmd/"+cfg.Name))
	return out
}

// gofmtSource re-formats generated Go source. The mechanical rewrite changes
// identifier lengths, and gofmt aligns consecutive composite-literal values and
// line comments to the widest entry in a run, so substituting a longer or
// shorter module path or service name leaves the alignment wrong. Without this
// pass a freshly generated repository fails its own fmt-check gate, which is
// the first promise the template makes.
//
// A rewrite that produces invalid Go is a defect in the skeleton, not something
// to paper over, so the parse error is returned rather than the unformatted
// bytes.
func gofmtSource(path string, out []byte) ([]byte, error) {
	if !strings.HasSuffix(path, ".go") {
		return out, nil
	}
	formatted, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("generated %s is not valid Go after the rewrite: %w", path, err)
	}
	return formatted, nil
}

// Render reads one skeleton file, renders it when it carries the template
// suffix, and applies the mechanical rewrite.
func Render(src fs.FS, e Entry, cfg *Config) ([]byte, error) {
	if e.Source == "" {
		return nil, fmt.Errorf("%s declares %q but the skeleton holds no such file",
			e.Fragment, e.Path)
	}
	raw, err := fs.ReadFile(src, e.Source)
	if err != nil {
		return nil, fmt.Errorf("read skeleton file %s: %w", e.Source, err)
	}
	if !strings.HasSuffix(e.Source, TemplateSuffix) {
		return gofmtSource(e.Path, substitute(raw, cfg))
	}
	t, err := template.New(path.Base(e.Source)).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse skeleton template %s: %w", e.Source, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, NewData(cfg)); err != nil {
		return nil, fmt.Errorf("render skeleton template %s: %w", e.Source, err)
	}
	return gofmtSource(e.Path, substitute(buf.Bytes(), cfg))
}
