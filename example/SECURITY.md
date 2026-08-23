# Security

## Reporting a vulnerability

Report a suspected vulnerability privately. Do not open a public issue, and do
not describe the defect in a public pull request.

Send the report to the address the repository owner publishes for security
contact. Include:

- What an attacker can do, and what they need to do it.
- The steps that reproduce it, with the exact request or input.
- The version, commit, or deployed build you observed it on.
- Any log line or response body that shows the effect.

You will receive an acknowledgement within three working days, an assessment
with a severity and a plan within ten working days, and a notification when the
fix ships. If a report goes unacknowledged for longer, send it again rather than
assuming it was received.

Report the defect before you test it against a deployed environment that is not
yours.

## Supported versions

The latest release of the default branch receives security fixes. An older
release receives a fix only when it is still deployed and the upgrade path is
blocked.

## What the service does with your data

The service redacts secret configuration values in every log record, in every
error body, and in the start-up record that reports the effective configuration.
An error response carries a request identifier and a client-safe explanation. It
never carries an internal message, a query, a host name, or a credential.

## Handling a report

A confirmed vulnerability is fixed on a private branch, released, and then
described publicly with the affected versions and the fixed version. The public
description follows the release, so an operator can upgrade before the defect is
searchable.
