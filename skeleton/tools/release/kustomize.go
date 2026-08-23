package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kustomization is one overlay file, held as a node tree so an edit preserves
// the comments that explain the placeholder it replaces.
type Kustomization struct {
	path string
	doc  yaml.Node
}

// LoadKustomization reads an overlay.
func LoadKustomization(path string) (*Kustomization, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	k := &Kustomization{path: path}
	if err := yaml.Unmarshal(data, &k.doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if k.root() == nil {
		return nil, fmt.Errorf("%s is not a mapping", path)
	}
	return k, nil
}

// root returns the document's mapping node.
func (k *Kustomization) root() *yaml.Node {
	if len(k.doc.Content) == 0 || k.doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return k.doc.Content[0]
}

// value returns the node for a top-level key, or nil.
func (k *Kustomization) value(key string) *yaml.Node {
	root := k.root()
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

// Namespace is the namespace the overlay applies into. The rollout reads it
// rather than taking it as an argument, so the manifest stays the one place
// the target's namespace is written.
func (k *Kustomization) Namespace() string {
	n := k.value("namespace")
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

// Resources lists the resource references the overlay names.
func (k *Kustomization) Resources() []string {
	seq := k.value("resources")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(seq.Content))
	for _, item := range seq.Content {
		out = append(out, item.Value)
	}
	return out
}

// ImageNames lists the image entries the overlay rewrites.
func (k *Kustomization) ImageNames() []string {
	seq := k.value("images")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, item := range seq.Content {
		if v := mapValue(item, "name"); v != nil {
			out = append(out, v.Value)
		}
	}
	return out
}

// SetImage points the named image entry at a registry reference and a digest.
// The applied manifest then names an immutable image, so what the cluster
// pulls cannot change under a tag that someone moved.
func (k *Kustomization) SetImage(name, newName, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("digest %q must be a sha256 reference", digest)
	}
	seq := k.value("images")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s declares no images list", k.path)
	}
	for _, item := range seq.Content {
		n := mapValue(item, "name")
		if n == nil || n.Value != name {
			continue
		}
		setMapValue(item, "newName", newName)
		setMapValue(item, "digest", digest)
		// A digest and a tag together are ambiguous, and kustomize refuses
		// the combination.
		deleteMapKey(item, "newTag")
		return nil
	}
	return fmt.Errorf("%s has no image entry named %q", k.path, name)
}

// Write saves the overlay with the indentation kustomize files use.
func (k *Kustomization) Write() error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&k.doc); err != nil {
		return fmt.Errorf("encode %s: %w", k.path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode %s: %w", k.path, err)
	}
	if err := os.WriteFile(k.path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", k.path, err)
	}
	return nil
}

// mapValue returns the value node for a key inside a mapping node.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMapValue sets a scalar key, appending it when it is absent.
func setMapValue(m *yaml.Node, key, value string) {
	if v := mapValue(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// deleteMapKey removes a key and its value.
func deleteMapKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
