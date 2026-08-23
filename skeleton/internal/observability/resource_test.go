package observability

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"example.com/service/internal/version"
)

// resourceAttributes indexes a resource by key.
func resourceAttributes(t *testing.T, res *resource.Resource) map[attribute.Key]string {
	t.Helper()
	attrs := map[attribute.Key]string{}
	for _, kv := range res.Attributes() {
		attrs[kv.Key] = kv.Value.String()
	}
	return attrs
}

// TestResourceIdentifiesTheBuild covers what makes a signal attributable: the
// resource names the service, the build it came from, and where it runs.
func TestResourceIdentifiesTheBuild(t *testing.T) {
	res, err := newResource("widget", "production")
	if err != nil {
		t.Fatalf("newResource: %v", err)
	}
	attrs := resourceAttributes(t, res)

	build := version.Info()
	expected := map[attribute.Key]string{
		semconv.ServiceNameKey:               "widget",
		semconv.ServiceVersionKey:            build.Version,
		commitKey:                            build.Commit,
		buildTimeKey:                         build.BuildTime,
		semconv.DeploymentEnvironmentNameKey: "production",
	}
	for key, want := range expected {
		if got := attrs[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	instance := attrs[semconv.ServiceInstanceIDKey]
	if !strings.HasSuffix(instance, "-"+strconv.Itoa(os.Getpid())) {
		t.Errorf("%s = %q, want it to end with this process identifier", semconv.ServiceInstanceIDKey, instance)
	}

	// The default resource contributes the telemetry SDK attributes, which is
	// how a backend tells one instrumentation library from another.
	if _, ok := attrs["telemetry.sdk.name"]; !ok {
		t.Error("resource lost the default telemetry SDK attributes")
	}
}

// TestResourceOmitsAnUnsetEnvironment keeps an empty label out of the backend,
// where it reads as a real environment named "".
func TestResourceOmitsAnUnsetEnvironment(t *testing.T) {
	res, err := newResource("widget", "")
	if err != nil {
		t.Fatalf("newResource: %v", err)
	}
	if _, ok := resourceAttributes(t, res)[semconv.DeploymentEnvironmentNameKey]; ok {
		t.Error("resource carries a deployment environment that was never set")
	}
}

// TestInstanceIDSeparatesReplicas covers the identifier two processes on one
// host must not share.
func TestInstanceIDSeparatesReplicas(t *testing.T) {
	id := instanceID()
	if id == "" {
		t.Fatal("instanceID is empty")
	}
	host, err := os.Hostname()
	if err == nil && host != "" && !strings.HasPrefix(id, host+"-") {
		t.Errorf("instanceID = %q, want it to start with the host name %q", id, host)
	}
}
