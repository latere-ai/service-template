package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Directories inside the deploy tree that are not targets. The base is not
// applied on its own, and the bootstrap directory holds one-time or immutable
// resources that the pipeline must never apply, because everything it applies
// runs on every release.
const (
	BaseDir      = "base"
	BootstrapDir = "bootstrap"
)

// DefaultDeployRoot is where the manifests live.
const DefaultDeployRoot = "deploy"

// KustomizationFile is the file every target directory carries.
const KustomizationFile = "kustomization.yaml"

// DefaultRolloutTimeout bounds the wait. On timeout the run captures the
// diagnostics and fails, rather than waiting for the job to be cancelled with
// nothing recorded.
const DefaultRolloutTimeout = 10 * time.Minute

// RolloutOptions is one deploy.
type RolloutOptions struct {
	// Root is the deploy tree, normally "deploy".
	Root string
	// Target names the overlay directory under Root.
	Target string
	// Service is the workload and container name.
	Service string
	// Image is the registry reference without a tag or digest.
	Image string
	// Digest pins the image.
	Digest string
	// Timeout bounds the rollout wait.
	Timeout time.Duration
	// Summary receives the markdown the run publishes, including the
	// diagnostics captured on a failed rollout.
	Summary io.Writer
}

// RolloutStatus is what the cluster reported once the rollout settled.
type RolloutStatus struct {
	Namespace          string
	Deployment         string
	Generation         int64
	ObservedGeneration int64
	DesiredReplicas    int
	ReadyReplicas      int
	Completed          time.Time
	Image              string
}

// deploymentState is the part of the workload object the rollout reads.
type deploymentState struct {
	Metadata struct {
		Name       string `json:"name"`
		Generation int64  `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration int64 `json:"observedGeneration"`
		ReadyReplicas      int   `json:"readyReplicas"`
		UpdatedReplicas    int   `json:"updatedReplicas"`
		Replicas           int   `json:"replicas"`
	} `json:"status"`
}

// TargetDir resolves an overlay directory and refuses the two directories that
// are not targets.
func TargetDir(root, target string) (string, error) {
	clean := strings.Trim(strings.TrimSpace(target), "/")
	if clean == "" || strings.Contains(clean, "/") || clean == "." || clean == ".." {
		return "", fmt.Errorf("target %q must be one directory name under %s/", target, root)
	}
	if clean == BaseDir {
		return "", fmt.Errorf("%s/%s is not a target; it is the shared base an overlay names", root, BaseDir)
	}
	if clean == BootstrapDir {
		return "", fmt.Errorf("%s/%s holds one-time resources and is never applied by the pipeline", root, BootstrapDir)
	}
	dir := filepath.Join(root, clean)
	if _, err := kustomizationPath(dir); err != nil {
		return "", fmt.Errorf("target %q is not applyable: %w", target, err)
	}
	return dir, nil
}

// Targets lists the overlays the pipeline may apply. The base and the
// bootstrap directory are excluded here, which is the single place that
// exclusion is decided.
func Targets(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var targets []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == BaseDir || e.Name() == BootstrapDir {
			continue
		}
		if _, err := kustomizationPath(filepath.Join(root, e.Name())); err != nil {
			continue
		}
		targets = append(targets, e.Name())
	}
	return targets, nil
}

// Rollout points the overlay at the released digest, applies it, proves the
// apply is idempotent, waits for the rollout, and asserts the cluster reached
// the state the manifest asked for.
func Rollout(ctx context.Context, r Runner, o RolloutOptions) (RolloutStatus, error) {
	if o.Root == "" {
		o.Root = DefaultDeployRoot
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultRolloutTimeout
	}
	dir, err := TargetDir(o.Root, o.Target)
	if err != nil {
		return RolloutStatus{}, err
	}

	overlay, err := kustomizationPath(dir)
	if err != nil {
		return RolloutStatus{}, err
	}
	k, err := LoadKustomization(overlay)
	if err != nil {
		return RolloutStatus{}, err
	}
	namespace := k.Namespace()
	if namespace == "" {
		return RolloutStatus{}, fmt.Errorf("%s declares no namespace", overlay)
	}
	if err := k.SetImage(o.Service, o.Image, o.Digest); err != nil {
		return RolloutStatus{}, err
	}
	if err := k.Write(); err != nil {
		return RolloutStatus{}, err
	}

	if err := applyIdempotent(ctx, r, dir, o.Summary); err != nil {
		return RolloutStatus{}, err
	}

	status, err := waitForRollout(ctx, r, o, dir, namespace)
	if err != nil {
		captureDiagnostics(ctx, r, namespace, o.Service, o.Summary)
		return status, err
	}
	return status, nil
}

// unchangedPattern matches the apply line for a resource the server did not
// have to change.
var unchangedPattern = regexp.MustCompile(`(?m)\s(unchanged|configured|created|serverside-applied)\s*$`)

// applyIdempotent applies the overlay and then applies it again. Everything
// the pipeline applies must be idempotent, because it applies on every
// release, and the second apply is what proves it: every resource must come
// back unchanged.
func applyIdempotent(ctx context.Context, r Runner, dir string, summary io.Writer) error {
	if res := r.Run(ctx, kubectl("apply", "-k", dir)); res.Err != nil {
		return fmt.Errorf("apply %s: %w", dir, res.Err)
	}
	second := r.Run(ctx, kubectl("apply", "-k", dir))
	if second.Err != nil {
		return fmt.Errorf("re-apply %s: %w", dir, second.Err)
	}

	var changed []string
	for line := range strings.SplitSeq(strings.TrimSpace(second.Output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := unchangedPattern.FindStringSubmatch(line)
		if m == nil || m[1] != "unchanged" {
			changed = append(changed, line)
		}
	}
	if len(changed) > 0 {
		section(summary, "Overlay is not idempotent", strings.Join(changed, "\n"))
		return fmt.Errorf("re-applying %s changed %d resource(s); every applied resource must be safe to re-apply:\n  %s",
			dir, len(changed), strings.Join(changed, "\n  "))
	}
	return nil
}

// waitForRollout waits for the controller and then reads the workload back.
// The wait alone is not proof: a rollout command can return while the
// controller is still acting on a previous generation.
func waitForRollout(ctx context.Context, r Runner, o RolloutOptions, dir, namespace string) (RolloutStatus, error) {
	status := RolloutStatus{
		Namespace:  namespace,
		Deployment: o.Service,
		Image:      o.Image + "@" + o.Digest,
	}
	wait := kubectl("rollout", "status", "deployment/"+o.Service,
		"-n", namespace, "--timeout="+durationSeconds(o.Timeout))
	if res := r.Run(ctx, wait); res.Err != nil {
		return status, fmt.Errorf("rollout of %s in %s did not complete within %s: %w",
			o.Service, namespace, o.Timeout, res.Err)
	}

	out, err := output(ctx, r, kubectl("get", "deployment", o.Service, "-n", namespace, "-o", "json"))
	if err != nil {
		return status, fmt.Errorf("read %s after rollout: %w", o.Service, err)
	}
	var state deploymentState
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		return status, fmt.Errorf("parse %s after rollout: %w", o.Service, err)
	}

	status.Generation = state.Metadata.Generation
	status.ObservedGeneration = state.Status.ObservedGeneration
	status.ReadyReplicas = state.Status.ReadyReplicas
	status.DesiredReplicas = state.Status.Replicas
	if state.Spec.Replicas != nil {
		status.DesiredReplicas = *state.Spec.Replicas
	}
	status.Completed = time.Now().UTC()

	if state.Status.ObservedGeneration != state.Metadata.Generation {
		return status, fmt.Errorf("the controller has observed generation %d of %s while the applied manifest is generation %d",
			state.Status.ObservedGeneration, o.Service, state.Metadata.Generation)
	}
	if status.ReadyReplicas != status.DesiredReplicas || status.DesiredReplicas == 0 {
		return status, fmt.Errorf("%s reports %d ready replicas of %d desired",
			o.Service, status.ReadyReplicas, status.DesiredReplicas)
	}
	return status, nil
}

// captureDiagnostics records what the failing target looked like. A rollout
// failure that leaves nothing in the run summary has to be reproduced by hand
// against a cluster that has already moved on.
func captureDiagnostics(ctx context.Context, r Runner, namespace, service string, summary io.Writer) {
	selector := "app.kubernetes.io/name=" + service
	steps := []struct {
		title string
		cmd   Command
	}{
		{"Pod status", kubectl("get", "pods", "-n", namespace, "-l", selector, "-o", "wide")},
		{"Recent events", kubectl("get", "events", "-n", namespace, "--sort-by=.lastTimestamp")},
		{"Pod logs", kubectl("logs", "-n", namespace, "-l", selector,
			"--all-containers", "--prefix", "--tail=50")},
	}
	for _, s := range steps {
		res := r.Run(ctx, s.cmd)
		body := strings.TrimSpace(res.Output)
		if res.Err != nil {
			body = strings.TrimSpace(body + "\n" + res.Err.Error())
		}
		if body == "" {
			body = "(no output)"
		}
		section(summary, s.title, body)
	}
}

// section writes one diagnostic block into the run summary.
func section(w io.Writer, title, body string) {
	if w == nil {
		return
	}
	printf(w, "\n#### %s\n\n```\n%s\n```\n", title, body)
}

// kubectl builds a kubectl command.
func kubectl(args ...string) Command { return Command{Name: "kubectl", Args: args} }

// durationSeconds renders a timeout the way kubectl expects it.
func durationSeconds(d time.Duration) string {
	return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
}
