# Security policy

## Supported versions

The `v1` line receives security fixes, as a new release that the moving `v1`
tag follows; older tags do not. A fix to a reusable workflow reaches a service
through its callers, which pin `@v1`. A fix to a generated file reaches a
service when it upgrades to the release that carries it.

## Reporting a vulnerability

Report vulnerabilities through GitHub private vulnerability reporting on this
repository, or by email to security@latere.ai. Include the affected version,
the impact, and the steps to reproduce.

Do not open a public issue for an unfixed vulnerability. Public issues about a
security defect must describe the impact and the affected surface only, and
must not carry a working exploit until a fix ships.

## Response

- Acknowledgment within three working days.
- An assessment and a planned fix date within ten working days.
- Public disclosure after the fix is released, with credit if you want it.
