package main

import (
	"os"
	"strings"
	"testing"
)

const overlaySource = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: service-production

resources:
  - ../base

# The pipeline rewrites this entry with the digest it built and verified.
images:
  - name: service
    newName: registry.invalid/service
    newTag: unreleased
`

func TestSetImagePinsByDigestAndDropsTheTag(t *testing.T) {
	path := writeFile(t, t.TempDir(), "kustomization.yaml", overlaySource)
	k, err := LoadKustomization(path)
	if err != nil {
		t.Fatalf("LoadKustomization: %v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := k.SetImage("service", "ghcr.io/owner/service", digest); err != nil {
		t.Fatalf("SetImage: %v", err)
	}
	if err := k.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	text := string(data)
	mustContain(t, text, "ghcr.io/owner/service", "the overlay")
	mustContain(t, text, digest, "the overlay")
	// A digest and a tag together are ambiguous, and kustomize refuses the
	// combination.
	if strings.Contains(text, "newTag") {
		t.Errorf("the tag survived the rewrite:\n%s", text)
	}
	// The comment explains what the entry is for, and an edit that drops it
	// leaves the next reader guessing.
	mustContain(t, text, "The pipeline rewrites this entry", "the overlay")
}

func TestKustomizationReadsNamespaceAndResources(t *testing.T) {
	path := writeFile(t, t.TempDir(), "kustomization.yaml", overlaySource)
	k, err := LoadKustomization(path)
	if err != nil {
		t.Fatalf("LoadKustomization: %v", err)
	}
	if k.Namespace() != "service-production" {
		t.Errorf("Namespace = %q", k.Namespace())
	}
	if got := k.Resources(); len(got) != 1 || got[0] != "../base" {
		t.Errorf("Resources = %v", got)
	}
	if got := k.ImageNames(); len(got) != 1 || got[0] != "service" {
		t.Errorf("ImageNames = %v", got)
	}
}

func TestSetImageRejectsAnUnknownEntryAndAnUnpinnedDigest(t *testing.T) {
	path := writeFile(t, t.TempDir(), "kustomization.yaml", overlaySource)
	k, err := LoadKustomization(path)
	if err != nil {
		t.Fatalf("LoadKustomization: %v", err)
	}
	if err := k.SetImage("other", "ghcr.io/owner/other", "sha256:"+strings.Repeat("a", 64)); err == nil {
		t.Error("SetImage accepted an image entry the overlay does not declare")
	}
	if err := k.SetImage("service", "ghcr.io/owner/service", "latest"); err == nil {
		t.Error("SetImage accepted a reference that is not a digest")
	}
}

func TestSetImageAddsMissingKeys(t *testing.T) {
	path := writeFile(t, t.TempDir(), "kustomization.yaml",
		"images:\n  - name: service\n")
	k, err := LoadKustomization(path)
	if err != nil {
		t.Fatalf("LoadKustomization: %v", err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	if err := k.SetImage("service", "ghcr.io/owner/service", digest); err != nil {
		t.Fatalf("SetImage: %v", err)
	}
	if err := k.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(path)
	mustContain(t, string(data), "newName: ghcr.io/owner/service", "the overlay")
	mustContain(t, string(data), digest, "the overlay")
}

func TestLoadKustomizationRejectsMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadKustomization(writeFile(t, dir, "a.yaml", "- one\n- two\n")); err == nil {
		t.Error("LoadKustomization accepted a sequence document")
	}
	if _, err := LoadKustomization(writeFile(t, dir, "b.yaml", "images: [\n")); err == nil {
		t.Error("LoadKustomization accepted unparsable YAML")
	}
	if _, err := LoadKustomization(dir + "/absent.yaml"); err == nil {
		t.Error("LoadKustomization accepted a file that does not exist")
	}
}

func TestSetImageReportsAnOverlayWithNoImagesList(t *testing.T) {
	path := writeFile(t, t.TempDir(), "kustomization.yaml", "namespace: service-production\n")
	k, err := LoadKustomization(path)
	if err != nil {
		t.Fatalf("LoadKustomization: %v", err)
	}
	if err := k.SetImage("service", "ghcr.io/owner/service", "sha256:"+strings.Repeat("c", 64)); err == nil {
		t.Fatal("SetImage accepted an overlay with no images list")
	}
	if k.Namespace() != "service-production" {
		t.Errorf("Namespace = %q", k.Namespace())
	}
	if k.Resources() != nil || k.ImageNames() != nil {
		t.Error("an overlay with no lists reported entries")
	}
}
