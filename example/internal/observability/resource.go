package observability

import (
	"errors"
	"os"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/example/reference-service/internal/version"
)

// Resource attribute keys the semantic conventions do not define. The commit
// and the build time make a telemetry record attributable to one build, which
// service.version alone cannot do while several builds share a tag.
const (
	commitKey    = attribute.Key("service.commit")
	buildTimeKey = attribute.Key("service.build_time")
)

// unknownInstance is the instance identifier used when the host name is
// unreadable. The process identifier still separates two instances on one host.
const unknownInstance = "unknown"

// newResource builds the resource every signal carries: who the service is,
// which build it runs, which instance it is, and where it is deployed.
//
// The version values come from the package the release pipeline stamps, so a
// span, a metric, and a log record all name the same build.
func newResource(serviceName, environment string) (*resource.Resource, error) {
	b := version.Info()

	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(b.Version),
		semconv.ServiceInstanceID(instanceID()),
		commitKey.String(b.Commit),
		buildTimeKey.String(b.BuildTime),
	}
	if environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentName(environment))
	}

	merged, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, attrs...))
	if err != nil {
		// The default resource and this package can follow different semantic
		// convention releases. Merge still returns every attribute in that
		// case, only without a schema URL, which is the right trade: dropping
		// the resource would cost the service identity of every signal.
		if errors.Is(err, resource.ErrSchemaURLConflict) && merged != nil {
			return merged, nil
		}
		return nil, err
	}
	return merged, nil
}

// instanceID identifies one running process of the service. Host name and
// process identifier together stay stable for the life of the process and
// differ between two replicas on one host.
func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = unknownInstance
	}
	return host + "-" + strconv.Itoa(os.Getpid())
}
