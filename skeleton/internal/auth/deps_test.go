package auth_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The gate: the service imports no identity-provider client library. The
// boundary is the Authenticator interface, so a provider is reached through an
// implementation the consumer owns. A provider client in the module makes the
// provider a dependency of every build, of every test, and of the template
// itself, which is the coupling the boundary exists to prevent.
//
// The check has two layers. The module file is the authoritative one: the
// module is self contained, so nothing can be imported that is not required
// there. The import walk is the second layer, and it catches a provider
// reached through a package that is already required.
var identityProviderModules = []string{
	// Hosted identity providers.
	"github.com/auth0/",
	"github.com/okta/",
	"github.com/clerk/",
	"github.com/descope/",
	"github.com/stytchauth/",
	"github.com/workos/",
	"github.com/supabase-community/",
	"github.com/ory/",
	"github.com/nerzal/gocloak",
	"firebase.google.com/go",
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider",
	"google.golang.org/api/idtoken",
	// Token and session libraries, which carry the same coupling: the token
	// format becomes a dependency of the core.
	"github.com/golang-jwt/",
	"github.com/lestrrat-go/jwx",
	"github.com/go-jose/",
	"gopkg.in/square/go-jose",
	"github.com/coreos/go-oidc",
	"golang.org/x/oauth2",
	"github.com/markbates/goth",
	"github.com/gorilla/sessions",
	// Policy engines, for the same reason on the authorization side.
	"github.com/casbin/",
	"github.com/open-policy-agent/",
}

// forbiddenImports returns the paths that name an identity provider.
func forbiddenImports(paths []string) []string {
	var found []string
	for _, path := range paths {
		lowered := strings.ToLower(path)
		for _, module := range identityProviderModules {
			if strings.HasPrefix(lowered, module) {
				found = append(found, path)
				break
			}
		}
	}
	return found
}

// TestForbiddenImportsMatcherReportsAProvider proves the gate fails when the
// thing it guards is present. Without it the module scan below would pass on
// an empty list and prove nothing.
func TestForbiddenImportsMatcherReportsAProvider(t *testing.T) {
	if len(identityProviderModules) == 0 {
		t.Fatal("the deny list is empty, so the gate asserts nothing")
	}
	found := forbiddenImports([]string{
		"example.com/service/internal/auth",
		"github.com/Auth0/go-auth0/management",
		"net/http",
		"github.com/golang-jwt/jwt/v5",
	})
	want := []string{"github.com/Auth0/go-auth0/management", "github.com/golang-jwt/jwt/v5"}
	if len(found) != len(want) {
		t.Fatalf("the matcher reported %v, want %v", found, want)
	}
	for i := range want {
		if found[i] != want[i] {
			t.Errorf("finding %d = %q, want %q", i, found[i], want[i])
		}
	}
}

func TestModuleRequiresNoIdentityProvider(t *testing.T) {
	root := moduleRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read the module file: %v", err)
	}
	required := requiredModules(string(src))
	if len(required) == 0 {
		t.Fatal("the module file lists no requirements, so the gate asserts nothing")
	}
	if found := forbiddenImports(required); len(found) > 0 {
		t.Fatalf("the module requires identity provider libraries %v; the core reaches a provider "+
			"through an auth.Authenticator instead", found)
	}
}

func TestNoPackageImportsAnIdentityProvider(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if parseErr != nil {
			// A file that does not parse fails the build on its own, and this
			// gate has nothing to say about it.
			//nolint:nilerr // the build reports the parse failure; this gate does not
			return nil
		}
		scanned++
		paths := make([]string, 0, len(file.Imports))
		for _, imported := range file.Imports {
			value, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				continue
			}
			paths = append(paths, value)
		}
		if found := forbiddenImports(paths); len(found) > 0 {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			t.Errorf("%s imports identity provider libraries %v", rel, found)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	if scanned == 0 {
		t.Fatal("the walk parsed no files, so the gate asserts nothing")
	}
}

// requiredModules returns the module paths named in a go.mod require list,
// both the block form and the single line form.
func requiredModules(src string) []string {
	var (
		required []string
		inBlock  bool
	)
	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		switch {
		case line == "require (":
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "":
			required = append(required, strings.Fields(line)[0])
		case strings.HasPrefix(line, "require "):
			required = append(required, strings.Fields(line)[1])
		}
	}
	return required
}

// moduleRoot walks up from the test's working directory to the module file.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
			return ""
		}
		dir = parent
	}
}
