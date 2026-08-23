package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Environment variables the smoke run reads. The target arrives through the
// environment so the same executable runs from the pipeline, from a
// maintainer's shell, and against a pre-production address with no argument
// parsing in between.
const (
	// EnvBaseURL is the live address, scheme included. It is the ingress
	// address and never a port forward, because the run must exercise the
	// routing and the certificate and not only the process.
	EnvBaseURL = "SMOKE_BASE_URL"
	// EnvTarget names the deployment target in the evidence block.
	EnvTarget = "SMOKE_TARGET"
	// EnvExpectVersion is the version the release built.
	EnvExpectVersion = "SMOKE_EXPECT_VERSION"
	// EnvExpectCommit is the full commit the release was tagged at.
	EnvExpectCommit = "SMOKE_EXPECT_COMMIT"
	// EnvExpectAsset is the hashed entry asset the release embedded, or the
	// literal "none" for a service that ships no frontend. It has no default:
	// an empty expectation would turn the check that catches a stale bundle
	// into a check that always passes.
	EnvExpectAsset = "SMOKE_EXPECT_ASSET"
	// EnvChecks points at the consumer's own assertions.
	EnvChecks = "SMOKE_CHECKS"
	// EnvWindow bounds the total retry window.
	EnvWindow = "SMOKE_WINDOW"
	// EnvBackoff is the first wait between attempts; it doubles up to a cap.
	EnvBackoff = "SMOKE_BACKOFF"
	// EnvRequestTimeout bounds one HTTP request.
	EnvRequestTimeout = "SMOKE_REQUEST_TIMEOUT"
	// EnvEvidence names the file the evidence block is appended to.
	EnvEvidence = "SMOKE_EVIDENCE"
	// EnvStepSummary is the run summary the pipeline provides. It is the
	// evidence destination when EnvEvidence is unset.
	EnvStepSummary = "GITHUB_STEP_SUMMARY"
)

// NoFrontend is the entry asset expectation for a service with no bundle.
const NoFrontend = "none"

// Defaults for the retry window. The window is short and bounded because a
// rollout completes slightly before the load balancer converges, and a long
// window would hide a service that is genuinely slow to become healthy.
const (
	DefaultWindow         = 90 * time.Second
	DefaultBackoff        = 1 * time.Second
	MaxBackoff            = 8 * time.Second
	DefaultRequestTimeout = 10 * time.Second
)

// Config is one smoke run.
type Config struct {
	BaseURL        string
	Target         string
	ExpectVersion  string
	ExpectCommit   string
	ExpectAsset    string
	ChecksPath     string
	Window         time.Duration
	Backoff        time.Duration
	RequestTimeout time.Duration
	EvidencePath   string
}

// HasFrontend reports whether the release carries a bundle whose entry asset
// the live document must reference.
func (c Config) HasFrontend() bool { return c.ExpectAsset != NoFrontend }

// LoadConfig reads the environment. Every required variable is named in the
// failure, so a misconfigured run says which one is missing instead of failing
// an assertion for the wrong reason.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		BaseURL:       strings.TrimRight(strings.TrimSpace(getenv(EnvBaseURL)), "/"),
		Target:        strings.TrimSpace(getenv(EnvTarget)),
		ExpectVersion: strings.TrimSpace(getenv(EnvExpectVersion)),
		ExpectCommit:  strings.TrimSpace(getenv(EnvExpectCommit)),
		ExpectAsset:   strings.TrimSpace(getenv(EnvExpectAsset)),
		ChecksPath:    strings.TrimSpace(getenv(EnvChecks)),
		EvidencePath:  strings.TrimSpace(getenv(EnvEvidence)),
	}
	if c.EvidencePath == "" {
		c.EvidencePath = strings.TrimSpace(getenv(EnvStepSummary))
	}
	if c.Target == "" {
		c.Target = "unnamed"
	}

	required := []struct {
		env, value string
	}{
		{EnvBaseURL, c.BaseURL},
		{EnvExpectVersion, c.ExpectVersion},
		{EnvExpectCommit, c.ExpectCommit},
		{EnvExpectAsset, c.ExpectAsset},
	}
	var missing []string
	for _, r := range required {
		if r.value == "" {
			missing = append(missing, r.env)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment: %s", strings.Join(missing, ", "))
	}

	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("%s must be an absolute URL, got %q", EnvBaseURL, c.BaseURL)
	}

	durations := []struct {
		env   string
		def   time.Duration
		field *time.Duration
	}{
		{EnvWindow, DefaultWindow, &c.Window},
		{EnvBackoff, DefaultBackoff, &c.Backoff},
		{EnvRequestTimeout, DefaultRequestTimeout, &c.RequestTimeout},
	}
	for _, d := range durations {
		v, err := duration(getenv(d.env), d.def)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", d.env, err)
		}
		*d.field = v
	}
	return c, nil
}

// duration parses an optional duration variable.
func duration(raw string, def time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %q as a duration: %w", raw, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("must be positive, got %s", v)
	}
	return v, nil
}
