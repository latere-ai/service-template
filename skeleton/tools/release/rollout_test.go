package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deployTree writes a minimal but complete deploy tree and returns its root.
func deployTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "deploy")
	writeFile(t, root, "base/kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
`)
	writeFile(t, root, "base/deployment.yaml", `apiVersion: apps/v1
kind: Deployment
metadata:
  name: service
spec:
  replicas: 2
  template:
    spec:
      securityContext:
        runAsNonRoot: true
      containers:
        - name: service
          image: service:unreleased
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
          livenessProbe:
            httpGet:
              path: /livez
          readinessProbe:
            httpGet:
              path: /readyz
`)
	writeFile(t, root, "production/kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: service-production
resources:
  - ../base
images:
  - name: service
    newName: registry.invalid/service
    digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
`)
	writeFile(t, root, "bootstrap/namespace.yaml", "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: service-production\n")
	return root
}

// deploymentJSON is what kubectl reports for a settled workload.
func deploymentJSON(generation, observed int64, desired, ready int) string {
	return fmt.Sprintf(
		`{"metadata":{"name":"service","generation":%d},"spec":{"replicas":%d},`+
			`"status":{"observedGeneration":%d,"readyReplicas":%d,"replicas":%d}}`,
		generation, desired, observed, ready, desired)
}

// healthyRunner answers the commands a successful rollout issues.
func healthyRunner(dir string) *fakeRunner {
	return &fakeRunner{stubs: []stub{
		{match: "kubectl apply -k " + dir, result: ok("deployment.apps/service unchanged\n")},
		{match: "kubectl rollout status", result: ok("deployment \"service\" successfully rolled out\n")},
		{match: "kubectl get deployment", result: ok(deploymentJSON(4, 4, 2, 2))},
	}}
}

func rolloutOptions(root string, summary *strings.Builder) RolloutOptions {
	return RolloutOptions{
		Root: root, Target: "production", Service: "service",
		Image:  "ghcr.io/owner/service",
		Digest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		// The wait is bounded so a stalled rollout fails with diagnostics
		// rather than waiting for the job to be cancelled.
		Timeout: 30 * time.Second,
		Summary: summary,
	}
}

func TestRolloutPinsTheImageAndReportsTheState(t *testing.T) {
	root := deployTree(t)
	var summary strings.Builder
	r := healthyRunner(filepath.Join(root, "production"))

	status, err := Rollout(context.Background(), r, rolloutOptions(root, &summary))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if status.ReadyReplicas != 2 || status.DesiredReplicas != 2 || status.Namespace != "service-production" {
		t.Errorf("status = %+v", status)
	}

	overlay, err := os.ReadFile(filepath.Join(root, "production", KustomizationFile))
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	mustContain(t, string(overlay), "ghcr.io/owner/service", "the overlay")
	mustContain(t, string(overlay), "sha256:1111", "the overlay")
	if strings.Contains(string(overlay), "registry.invalid") {
		t.Errorf("the placeholder registry survived the rewrite:\n%s", overlay)
	}
}

// Everything the pipeline applies runs on every release, so a resource the
// second apply has to change is a resource that would drift on every run.
func TestRolloutFailsWhenTheOverlayIsNotIdempotent(t *testing.T) {
	root := deployTree(t)
	dir := filepath.Join(root, "production")
	var summary strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "kubectl apply -k " + dir, result: ok("deployment.apps/service configured\njob.batch/migrate created\n")},
	}}

	_, err := Rollout(context.Background(), r, rolloutOptions(root, &summary))
	if err == nil {
		t.Fatal("Rollout accepted an overlay that changes on every apply")
	}
	mustContain(t, err.Error(), "job.batch/migrate created", "the failure")
	mustContain(t, summary.String(), "not idempotent", "the summary")
}

// A stalled rollout has to be diagnosable from the run alone.
func TestStalledRolloutCapturesPodStatusEventsAndLogs(t *testing.T) {
	root := deployTree(t)
	dir := filepath.Join(root, "production")
	var summary strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "kubectl apply -k " + dir, result: ok("deployment.apps/service unchanged\n")},
		{match: "kubectl rollout status", result: fails("timed out waiting for the condition")},
		{match: "kubectl get pods", result: ok("service-abc 0/1 CrashLoopBackOff 4 2m\n")},
		{match: "kubectl get events", result: ok("Warning BackOff restarting failed container\n")},
		{match: "kubectl logs", result: ok("panic: dial tcp 10.0.0.1:5432: connection refused\n")},
	}}

	_, err := Rollout(context.Background(), r, rolloutOptions(root, &summary))
	if err == nil {
		t.Fatal("Rollout reported success for a stalled rollout")
	}
	for _, want := range []string{
		"Pod status", "CrashLoopBackOff",
		"Recent events", "BackOff restarting",
		"Pod logs", "connection refused",
	} {
		mustContain(t, summary.String(), want, "the run summary")
	}
}

// The wait alone is not proof: the controller can return while it is still
// acting on a previous generation.
func TestRolloutFailsWhenTheControllerIsBehind(t *testing.T) {
	root := deployTree(t)
	dir := filepath.Join(root, "production")
	var summary strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "kubectl apply -k " + dir, result: ok("deployment.apps/service unchanged\n")},
		{match: "kubectl rollout status", result: ok("ok")},
		{match: "kubectl get deployment", result: ok(deploymentJSON(5, 4, 2, 2))},
		{match: "kubectl get pods", result: ok("")},
		{match: "kubectl get events", result: ok("")},
		{match: "kubectl logs", result: ok("")},
	}}

	_, err := Rollout(context.Background(), r, rolloutOptions(root, &summary))
	if err == nil {
		t.Fatal("Rollout accepted a workload whose observed generation is behind")
	}
	mustContain(t, err.Error(), "generation", "the failure")
}

func TestRolloutFailsWhenReplicasAreMissing(t *testing.T) {
	root := deployTree(t)
	dir := filepath.Join(root, "production")
	var summary strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "kubectl apply -k " + dir, result: ok("deployment.apps/service unchanged\n")},
		{match: "kubectl rollout status", result: ok("ok")},
		{match: "kubectl get deployment", result: ok(deploymentJSON(4, 4, 3, 1))},
		{match: "kubectl get pods", result: ok("")},
		{match: "kubectl get events", result: ok("")},
		{match: "kubectl logs", result: ok("")},
	}}

	_, err := Rollout(context.Background(), r, rolloutOptions(root, &summary))
	if err == nil {
		t.Fatal("Rollout accepted one ready replica of three")
	}
	mustContain(t, err.Error(), "1 ready replicas of 3", "the failure")
}

// The bootstrap directory holds one-time resources, and the pipeline applies
// on every release.
func TestTargetDirRefusesTheDirectoriesThatAreNotTargets(t *testing.T) {
	root := deployTree(t)
	for _, target := range []string{BaseDir, BootstrapDir, "", "..", "production/extra", "absent"} {
		if _, err := TargetDir(root, target); err == nil {
			t.Errorf("TargetDir accepted %q", target)
		}
	}
	if _, err := TargetDir(root, "production"); err != nil {
		t.Errorf("TargetDir rejected a real target: %v", err)
	}
}

func TestTargetsExcludesTheBaseAndTheBootstrapDirectory(t *testing.T) {
	targets, err := Targets(deployTree(t))
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 || targets[0] != "production" {
		t.Fatalf("Targets = %v, want only the overlay", targets)
	}
}

func TestRolloutNeverIssuesACommandAgainstTheBootstrapDirectory(t *testing.T) {
	root := deployTree(t)
	var summary strings.Builder
	r := healthyRunner(filepath.Join(root, "production"))
	if _, err := Rollout(context.Background(), r, rolloutOptions(root, &summary)); err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	for _, call := range r.calls {
		if strings.Contains(call, BootstrapDir) {
			t.Errorf("the rollout issued %q, which reaches into the bootstrap directory", call)
		}
	}
}

func TestRolloutRefusesAnUnpinnedImage(t *testing.T) {
	root := deployTree(t)
	var summary strings.Builder
	o := rolloutOptions(root, &summary)
	o.Digest = "latest"
	if _, err := Rollout(context.Background(), &fakeRunner{}, o); err == nil {
		t.Fatal("Rollout accepted an image that is not pinned by digest")
	}
}

func TestDurationSeconds(t *testing.T) {
	if got := durationSeconds(90 * time.Second); got != "90s" {
		t.Errorf("durationSeconds = %q", got)
	}
}

func TestRolloutReportsAnOverlayWithoutTheServiceImageEntry(t *testing.T) {
	root := deployTree(t)
	var summary strings.Builder
	o := rolloutOptions(root, &summary)
	o.Service = "other"
	if _, err := Rollout(context.Background(), &fakeRunner{}, o); err == nil {
		t.Fatal("Rollout accepted an overlay with no entry for the workload")
	}
}

func TestRolloutReportsAnOverlayWithoutANamespace(t *testing.T) {
	root := deployTree(t)
	writeFile(t, root, "production/kustomization.yaml",
		"resources:\n  - ../base\nimages:\n  - name: service\n")
	var summary strings.Builder
	if _, err := Rollout(context.Background(), &fakeRunner{}, rolloutOptions(root, &summary)); err == nil {
		t.Fatal("Rollout applied a target with no namespace")
	}
}

func TestRolloutReportsUnreadableWorkloadState(t *testing.T) {
	root := deployTree(t)
	dir := filepath.Join(root, "production")
	var summary strings.Builder
	r := &fakeRunner{stubs: []stub{
		{match: "kubectl apply -k " + dir, result: ok("deployment.apps/service unchanged\n")},
		{match: "kubectl rollout status", result: ok("ok")},
		{match: "kubectl get deployment", result: ok("not json")},
		{match: "kubectl get pods", result: ok("")},
		{match: "kubectl get events", result: fails("forbidden")},
		{match: "kubectl logs", result: ok("")},
	}}
	if _, err := Rollout(context.Background(), r, rolloutOptions(root, &summary)); err == nil {
		t.Fatal("Rollout accepted workload state it could not parse")
	}
	// A diagnostic command that itself fails still has to leave a record.
	mustContain(t, summary.String(), "forbidden", "the summary")
	mustContain(t, summary.String(), "(no output)", "the summary")
}

func TestRolloutFailsWhenTheFirstApplyFails(t *testing.T) {
	root := deployTree(t)
	var summary strings.Builder
	r := &fakeRunner{stubs: []stub{{match: "kubectl apply", result: fails("forbidden")}}}
	if _, err := Rollout(context.Background(), r, rolloutOptions(root, &summary)); err == nil {
		t.Fatal("Rollout continued after a failed apply")
	}
}
