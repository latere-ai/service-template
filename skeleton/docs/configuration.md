# Configuration

Every input the service reads is listed here. The service reads its
configuration once at start-up, so a changed value takes effect at the next
restart.

## Where a value comes from

The first source that supplies a value wins:

1. A command-line flag.
2. An environment variable.
3. The file named by the `<NAME>_FILE` variable, with surrounding
   whitespace removed. This is how a mounted secret is read.
4. The default declared below.

A missing required value, an unparsable value, or a value outside its allowed
range stops start-up, and the failure names every problem it found rather than
the first.

At start-up the service logs the effective values with the source of each one,
so an operator can see which values were defaulted. Secrets are redacted in
that record and in every other record.

<!-- Generated from the code by "make docs". Do not edit. -->

## Settings

| Variable | Flag | Type | Default | Effect |
| --- | --- | --- | --- | --- |
| `SERVICE_NAME` | `-service-name` | string | `service` | Name reported in telemetry and logs. |
| `ENVIRONMENT` | `-environment` | string | `development` | Deployment environment: development, staging, or production. |
| `ADDR` | `-addr` | string | `:8080` | HTTP listen address as host:port. |
| `LOG_LEVEL` | `-log-level` | log level | `info` | Minimum log level: debug, info, warn, or error. |
| `LOG_FORMAT` | `-log-format` | string | `json` | Log handler: json or text. |
| `HTTP_READ_HEADER_TIMEOUT` | `-http-read-header-timeout` | duration | `5s` | Deadline for reading request headers. |
| `HTTP_READ_TIMEOUT` | `-http-read-timeout` | duration | `30s` | Deadline for reading the whole request. |
| `HTTP_WRITE_TIMEOUT` | `-http-write-timeout` | duration | `30s` | Deadline for writing the response. |
| `HTTP_IDLE_TIMEOUT` | `-http-idle-timeout` | duration | `120s` | Idle keep-alive timeout. |
| `DRAIN_DELAY` | `-drain-delay` | duration | `5s` | Wait after SIGTERM before refusing new connections. |
| `GRACE_PERIOD` | `-grace-period` | duration | `30s` | Time in-flight requests may finish after the listener stops. |
| `STOP_TIMEOUT` | `-stop-timeout` | duration | `15s` | Time one component has to stop before it is abandoned. |
| `READY_CHECK_TIMEOUT` | `-ready-check-timeout` | duration | `2s` | Deadline for one readiness check. |
| `DATABASE_URL` | `-database-url` | secret | empty | PostgreSQL connection string. Empty disables the database. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `-otel-exporter-otlp-endpoint` | string | empty | OTLP collector base URL. Empty disables telemetry export. |
| `OTEL_EXPORTER_OTLP_HEADERS` | `-otel-exporter-otlp-headers` | secret | empty | OTLP headers as comma-separated key=value pairs. |
| `OTEL_TRACES_SAMPLE_RATIO` | `-otel-traces-sample-ratio` | number | `1.0` | Head trace sampling ratio between 0 and 1. |

## Required values

Every value has a default or is optional, so the service starts with no configuration at all.

## Secrets

These carry credentials. Mount each one as a file and set `<NAME>_FILE` to its path, so the value is not visible in the process environment. Their values are redacted in every log record.

- `DATABASE_URL`
- `OTEL_EXPORTER_OTLP_HEADERS`

## The example file

`.env.example` holds the same set with the defaults filled in.
Copy it to `.env` and edit the values. It is generated from the same struct as this
reference, so the two cannot disagree.
