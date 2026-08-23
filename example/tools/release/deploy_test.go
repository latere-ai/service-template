package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedDeployTreeHoldsTheManifestContract(t *testing.T) {
	// The name is derived rather than stated. The template's own copy is
	// checked under "service" and a generated repository's copy under its own
	// name, which is what makes this one gate for both trees.
	service, err := ServiceName(repoRoot)
	if err != nil {
		t.Fatalf("ServiceName: %v", err)
	}
	problems, err := CheckDeploy(deployPath, service)
	if err != nil {
		t.Fatalf("CheckDeploy: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("the shipped deploy tree breaks the contract:\n  %s", strings.Join(problems, "\n  "))
	}
}

// The pipeline sets the image by the service name, and the runtime image is
// built for a non-root user on a read-only root filesystem. Each of these
// mutations breaks one of those properties.
func TestCheckDeployCatchesEachBrokenProperty(t *testing.T) {
	cases := map[string]struct {
		file, from, to, want string
	}{
		"workload renamed": {
			"base/deployment.yaml", "name: service\n", "name: api\n",
			"workload is named",
		},
		"container renamed": {
			"base/deployment.yaml", "- name: service\n", "- name: app\n",
			"main container is named",
		},
		"writable root filesystem": {
			"base/deployment.yaml", "readOnlyRootFilesystem: true", "readOnlyRootFilesystem: false",
			"root filesystem is not read only",
		},
		"root user allowed": {
			"base/deployment.yaml", "runAsNonRoot: true", "runAsNonRoot: false",
			"non-root user",
		},
		"escalation allowed": {
			"base/deployment.yaml", "allowPrivilegeEscalation: false", "allowPrivilegeEscalation: true",
			"privilege escalation",
		},
		"capabilities kept": {
			"base/deployment.yaml", "- ALL", "- NET_BIND_SERVICE",
			"drop all capabilities",
		},
		"probe path changed": {
			"base/deployment.yaml", "path: /readyz", "path: /health",
			"/readyz",
		},
		"overlay without a namespace": {
			"production/kustomization.yaml", "namespace: service-production\n", "",
			"no namespace",
		},
		"overlay without the base": {
			"production/kustomization.yaml", "- ../base", "- ../other",
			"does not name ../base",
		},
		"overlay reaching into bootstrap": {
			"production/kustomization.yaml", "- ../base", "- ../base\n  - ../bootstrap",
			"never apply",
		},
		"overlay without the image entry": {
			"production/kustomization.yaml", "- name: service", "- name: other",
			"no image entry",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := deployTree(t)
			path := filepath.Join(root, c.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			replaced := strings.Replace(string(data), c.from, c.to, 1)
			if replaced == string(data) {
				t.Fatalf("the mutation %q was not applied", c.from)
			}
			if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}

			problems, err := CheckDeploy(root, "service")
			if err != nil {
				t.Fatalf("CheckDeploy: %v", err)
			}
			if !problemsContain(problems, c.want) {
				t.Fatalf("no problem mentions %q; got %v", c.want, problems)
			}
		})
	}
}

func TestCheckDeployReportsATreeWithNoTarget(t *testing.T) {
	root := deployTree(t)
	if err := os.RemoveAll(filepath.Join(root, "production")); err != nil {
		t.Fatalf("remove the overlay: %v", err)
	}
	problems, err := CheckDeploy(root, "service")
	if err != nil {
		t.Fatalf("CheckDeploy: %v", err)
	}
	if !problemsContain(problems, "no target overlay") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestCheckDeployReportsABaseWithNoWorkload(t *testing.T) {
	root := deployTree(t)
	if err := os.Remove(filepath.Join(root, "base/deployment.yaml")); err != nil {
		t.Fatalf("remove the workload: %v", err)
	}
	problems, err := CheckDeploy(root, "service")
	if err != nil {
		t.Fatalf("CheckDeploy: %v", err)
	}
	if !problemsContain(problems, "no Deployment") {
		t.Fatalf("problems = %v", problems)
	}
}

// The tree ships with the service name unrendered, so the check has to read
// both the template's copy and a generated repository's copy.
func TestReadObjectsRendersTheNamePlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "deployment.yaml.tmpl",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: {{ .Name }}\n")
	objects, err := ReadObjects(dir, "widgets")
	if err != nil {
		t.Fatalf("ReadObjects: %v", err)
	}
	if len(objects) != 1 || objects[0].Metadata.Name != "widgets" {
		t.Fatalf("objects = %+v", objects)
	}
}

func TestReadObjectsReportsUnparsableManifests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "broken.yaml", "kind: [\n")
	if _, err := ReadObjects(dir, "service"); err == nil {
		t.Fatal("ReadObjects accepted a manifest it could not parse")
	}
}

func TestServiceNameFollowsTheSingleCommandDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cmd/widgets/main.go", "package main\n")
	name, err := ServiceName(root)
	if err != nil || name != "widgets" {
		t.Fatalf("ServiceName = %q, %v", name, err)
	}

	writeFile(t, root, "cmd/other/main.go", "package main\n")
	if _, err := ServiceName(root); err == nil {
		t.Error("ServiceName resolved an ambiguous layout")
	}

	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "cmd"), 0o755); err != nil {
		t.Fatalf("create cmd/: %v", err)
	}
	if _, err := ServiceName(empty); err == nil {
		t.Error("ServiceName resolved a tree with no command")
	}
	if _, err := ServiceName(t.TempDir()); err == nil {
		t.Error("ServiceName resolved a tree with no cmd directory")
	}
}

// The skeleton's own layout is the case every consumer starts from.
func TestServiceNameOnTheShippedTree(t *testing.T) {
	name, err := ServiceName(repoRoot)
	if err != nil {
		t.Fatalf("ServiceName: %v", err)
	}
	if name == "" {
		t.Fatal("ServiceName returned an empty name")
	}
}
