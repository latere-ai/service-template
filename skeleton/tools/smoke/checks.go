package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultChecksPath is where the consumer's own assertions live when the
// environment names no other file.
const DefaultChecksPath = "tools/smoke/checks.yaml"

// Check is one consumer assertion against a real endpoint. It is data rather
// than code so the evidence block can print what was asked as well as what
// came back.
type Check struct {
	// Name identifies the check in the evidence block.
	Name string `yaml:"name"`
	// Path is appended to the base URL.
	Path string `yaml:"path"`
	// Method defaults to GET.
	Method string `yaml:"method"`
	// Status is the required response status, and defaults to 200.
	Status int `yaml:"status"`
	// Contains is an optional substring the body must hold.
	Contains string `yaml:"contains"`
	// Header carries request headers, for example an Accept type.
	Header map[string]string `yaml:"header"`
}

// checksFile is the document shape.
type checksFile struct {
	Checks []Check `yaml:"checks"`
}

// LoadChecks reads the consumer assertions. A file named explicitly must
// exist, because a typo in the path would otherwise silently reduce the smoke
// run to the template's own assertions. The default path is allowed to be
// absent, which is the state of a service that has no endpoints yet.
func LoadChecks(path string) ([]Check, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultChecksPath
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read checks %s: %w", path, err)
	}

	var doc checksFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse checks %s: %w", path, err)
	}
	for i := range doc.Checks {
		c := &doc.Checks[i]
		if strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("%s: check %d has no name", path, i+1)
		}
		if !strings.HasPrefix(c.Path, "/") {
			return nil, fmt.Errorf("%s: check %q has path %q, which must start with a slash", path, c.Name, c.Path)
		}
		if c.Method == "" {
			c.Method = "GET"
		}
		if c.Status == 0 {
			c.Status = 200
		}
	}
	return doc.Checks, nil
}
