# Interface reference

The service serves JSON over HTTP. This document lists the endpoints, the shape
of an error, and the rule that decides what may change under a running client.

<!-- Generated from the code by "make docs". Do not edit. -->

## Versioning

Every application endpoint sits under `/v1`. Inside one major version only additive
change is allowed: a new endpoint, a new optional request field, a new response field.
Removing a field, narrowing a type, or making an optional field required is a new
major version, served beside the old one at its own prefix.

A client ignores response fields it does not know, because new ones appear without a
version change.

An endpoint on its way out answers with these headers before it stops answering:

| Header | Meaning |
| --- | --- |
| `Deprecation` | When the endpoint was announced as deprecated. |
| `Sunset` | When the endpoint stops answering. |
| `Link` with `rel="successor-version"` | The endpoint that replaces it. |
| `Link` with `rel="deprecation"` | The document describing the change. |

## Application endpoints

The baseline serves no application endpoint. An endpoint appears here when the
service registers it in its route table.

## Operational endpoints

| Method | Path | Access | Purpose |
| --- | --- | --- | --- |
| GET | `/livez` | public | Reports that the process is running. It answers while the process is draining. |
| GET | `/readyz` | public | Reports that every registered dependency is reachable. It answers 503 while the process is draining or while a dependency is down, and names the failing dependency in the body. |
| GET | `/version` | public | Reports the build identity of the running binary: version, commit, build time, and asset hash. |

## Request identification

Send `X-Request-Id` to correlate a request with your own logs. The service accepts the value
when it is present, generates one when it is not, and returns it on every response,
including every error.

## Errors

Every failure answers with the same body, served as `application/problem+json; charset=utf-8`. One shape means a client
parses one thing, whichever endpoint failed.

```json
{
  "type": "https://errors.example.com/bad-request",
  "title": "Bad Request",
  "status": 400,
  "detail": "body is not valid JSON",
  "instance": "req_01J0000000000000000000"
}
```

### Error members

| Member | Type | Presence | Meaning |
| --- | --- | --- | --- |
| `type` | string | always | URI that identifies the error class. It is stable, and a client branches on it. |
| `title` | string | always | Short, stable summary of the error class. |
| `status` | number | always | HTTP status code, repeated in the body so a logged body is self-contained. |
| `detail` | string | when it applies | Explanation of this occurrence. It is safe to show to a user and is never an internal message. |
| `instance` | string | when it applies | Request identifier of the failed request. Quote it in a bug report; it joins the response to the server logs and to the trace. |
| `errors` | array of objects | when it applies | Rejected input fields, present on a validation failure. |

### Field errors

Each entry of `errors` names one rejected input field.

| Member | Type | Presence | Meaning |
| --- | --- | --- | --- |
| `field` | string | always | Name of the rejected input field, in the same spelling the request used. |
| `code` | string | always | Stable machine token for the reason, such as `required` or `format`. |
| `detail` | string | when it applies | What is wrong with the value. It is safe to show to a user. |

### Status codes

A 4xx means the request must change before it is retried. A 5xx means the request may
be retried. The service also answers `499` when the client disconnects before the
response is produced, which is neither a fault of the service nor a request it refused.
