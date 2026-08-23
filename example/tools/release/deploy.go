package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// namePlaceholder is what an unrendered manifest carries where the service
// name goes. The deploy tree is a seed file set, so a consumer's copy holds
// the real name and the template's copy holds the placeholder; the contract
// check reads both.
var namePlaceholder = regexp.MustCompile(`\{\{-?\s*\.Name\s*-?\}\}`)

// ServiceName derives the workload name from the single entry point under
// cmd/, so the layout is declared in one place and the manifests, the make
// targets, and the pipeline all agree without a second setting.
func ServiceName(root string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return "", fmt.Errorf("read cmd/: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("cmd/ holds no command directory")
	case 1:
		return names[0], nil
	default:
		sort.Strings(names)
		return "", fmt.Errorf("cmd/ holds %d command directories (%s); the workload name is ambiguous",
			len(names), strings.Join(names, ", "))
	}
}

// Object is the part of a manifest the contract check reads.
type Object struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Replicas *int `yaml:"replicas"`
		Template struct {
			Spec struct {
				SecurityContext struct {
					RunAsNonRoot *bool `yaml:"runAsNonRoot"`
				} `yaml:"securityContext"`
				Containers []Container `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
	// File is where the object was read from.
	File string `yaml:"-"`
}

// Container is the part of a container the contract check reads.
type Container struct {
	Name            string `yaml:"name"`
	Image           string `yaml:"image"`
	SecurityContext struct {
		ReadOnlyRootFilesystem   *bool `yaml:"readOnlyRootFilesystem"`
		AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
		Capabilities             struct {
			Drop []string `yaml:"drop"`
		} `yaml:"capabilities"`
	} `yaml:"securityContext"`
	LivenessProbe  Probe `yaml:"livenessProbe"`
	ReadinessProbe Probe `yaml:"readinessProbe"`
}

// Probe is one HTTP probe.
type Probe struct {
	HTTPGet struct {
		Path string `yaml:"path"`
	} `yaml:"httpGet"`
}

// ReadObjects loads every manifest in a directory, rendering the service name
// placeholder so the same check runs against the template's copy and against a
// generated repository's copy.
func ReadObjects(dir, service string) ([]Object, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var objects []Object
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isManifest(name) || strings.HasPrefix(name, "kustomization.") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		rendered := namePlaceholder.ReplaceAll(data, []byte(service))
		dec := yaml.NewDecoder(strings.NewReader(string(rendered)))
		for {
			var obj Object
			err := dec.Decode(&obj)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			if obj.Kind == "" {
				continue
			}
			obj.File = path
			objects = append(objects, obj)
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].File < objects[j].File })
	return objects, nil
}

// isManifest reports whether a file name is a manifest, with or without the
// template suffix the generator strips.
func isManifest(name string) bool {
	base := strings.TrimSuffix(name, ".tmpl")
	return strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")
}

// kustomizationPath finds the overlay file in a directory, with or without the
// template suffix.
func kustomizationPath(dir string) (string, error) {
	for _, candidate := range []string{KustomizationFile, KustomizationFile + ".tmpl"} {
		path := filepath.Join(dir, candidate)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s holds no %s", dir, KustomizationFile)
}

// CheckDeploy reports every way the deploy tree breaks the contract the
// pipeline depends on. It returns the problems rather than the first one, so
// one run fixes the tree instead of one problem per run.
func CheckDeploy(root, service string) ([]string, error) {
	var problems []string

	base := filepath.Join(root, BaseDir)
	objects, err := ReadObjects(base, service)
	if err != nil {
		return nil, err
	}
	problems = append(problems, checkWorkload(objects, service)...)

	targets, err := Targets(root)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		problems = append(problems, fmt.Sprintf("%s holds no target overlay", root))
	}
	for _, target := range targets {
		problems = append(problems, checkOverlay(root, target, service)...)
	}

	sort.Strings(problems)
	return problems, nil
}

// checkWorkload asserts the properties the pipeline and the runtime image
// depend on: the workload and its main container carry the service name, and
// the container runs with the restrictions the image was built for.
func checkWorkload(objects []Object, service string) []string {
	var problems []string
	var found bool
	for _, obj := range objects {
		if obj.Kind != "Deployment" {
			continue
		}
		found = true
		if obj.Metadata.Name != service {
			problems = append(problems, fmt.Sprintf("%s: workload is named %q, the pipeline sets the image on %q",
				obj.File, obj.Metadata.Name, service))
		}
		containers := obj.Spec.Template.Spec.Containers
		if len(containers) == 0 {
			problems = append(problems, fmt.Sprintf("%s: the workload declares no container", obj.File))
			continue
		}
		main := containers[0]
		if main.Name != service {
			problems = append(problems, fmt.Sprintf("%s: main container is named %q, the pipeline sets the image on %q",
				obj.File, main.Name, service))
		}
		if !isTrue(obj.Spec.Template.Spec.SecurityContext.RunAsNonRoot) {
			problems = append(problems, fmt.Sprintf("%s: the pod does not require a non-root user", obj.File))
		}
		if !isTrue(main.SecurityContext.ReadOnlyRootFilesystem) {
			problems = append(problems, fmt.Sprintf("%s: the container root filesystem is not read only", obj.File))
		}
		if !isFalse(main.SecurityContext.AllowPrivilegeEscalation) {
			problems = append(problems, fmt.Sprintf("%s: the container does not deny privilege escalation", obj.File))
		}
		if !dropsAll(main.SecurityContext.Capabilities.Drop) {
			problems = append(problems, fmt.Sprintf("%s: the container does not drop all capabilities", obj.File))
		}
		if main.LivenessProbe.HTTPGet.Path != "/livez" {
			problems = append(problems, fmt.Sprintf("%s: liveness probe reads %q, the runtime contract fixes /livez",
				obj.File, main.LivenessProbe.HTTPGet.Path))
		}
		if main.ReadinessProbe.HTTPGet.Path != "/readyz" {
			problems = append(problems, fmt.Sprintf("%s: readiness probe reads %q, the runtime contract fixes /readyz",
				obj.File, main.ReadinessProbe.HTTPGet.Path))
		}
	}
	if !found {
		problems = append(problems, "the base holds no Deployment")
	}
	return problems
}

// checkOverlay asserts a target is applyable on every release: it names the
// base, sets a namespace, carries the image entry the pipeline rewrites, and
// never reaches into the bootstrap directory.
func checkOverlay(root, target, service string) []string {
	dir := filepath.Join(root, target)
	path, err := kustomizationPath(dir)
	if err != nil {
		return []string{err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read %s: %v", path, err)}
	}
	rendered := namePlaceholder.ReplaceAll(data, []byte(service))

	// The overlay is staged rendered, because LoadKustomization reads a file
	// and the template's own copy still carries the name placeholder.
	staged, err := os.CreateTemp("", "overlay-*.yaml")
	if err != nil {
		return []string{fmt.Sprintf("stage %s: %v", path, err)}
	}
	tmp := staged.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := staged.Write(rendered); err != nil {
		_ = staged.Close()
		return []string{fmt.Sprintf("stage %s: %v", path, err)}
	}
	if err := staged.Close(); err != nil {
		return []string{fmt.Sprintf("stage %s: %v", path, err)}
	}

	k, err := LoadKustomization(tmp)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}

	var problems []string
	if k.Namespace() == "" {
		problems = append(problems, fmt.Sprintf("%s: no namespace, so the target is not isolated", path))
	}
	resources := k.Resources()
	if !slices.Contains(resources, "../"+BaseDir) {
		problems = append(problems, fmt.Sprintf("%s: does not name ../%s", path, BaseDir))
	}
	for _, res := range resources {
		if strings.Contains(res, BootstrapDir) {
			problems = append(problems, fmt.Sprintf("%s: names %s, which the pipeline must never apply", path, res))
		}
	}
	if !slices.Contains(k.ImageNames(), service) {
		problems = append(problems, fmt.Sprintf("%s: no image entry named %q for the pipeline to rewrite", path, service))
	}
	return problems
}

// isFalse reports whether an optional boolean is present and false. An absent
// value is not false: a security setting that relies on a default is a
// security setting nobody can read from the manifest.
func isFalse(b *bool) bool { return b != nil && !*b }

// isTrue reports whether an optional boolean is present and true.
func isTrue(b *bool) bool { return b != nil && *b }

// dropsAll reports whether the capability list drops everything.
func dropsAll(drop []string) bool {
	for _, c := range drop {
		if strings.EqualFold(c, "ALL") {
			return true
		}
	}
	return false
}
