# Security Policy

## Reporting a vulnerability

If you believe you have found a security vulnerability in **oapi**, please report
it **privately**. Do not open a public GitHub issue, pull request, or discussion
for security problems.

Email **security@antlss.dev** with:

- a description of the vulnerability and its impact,
- the affected module and version (or commit),
- steps to reproduce, ideally a minimal proof of concept,
- any suggested remediation if you have one.

Please give us a reasonable opportunity to investigate and release a fix before
any public disclosure. We support coordinated disclosure and will credit
reporters who wish to be acknowledged.

## Response expectations

- **Acknowledgement** — we aim to acknowledge a report within 3 business days.
- **Assessment** — we aim to provide an initial assessment (severity, whether it
  is accepted) within 7 business days.
- **Fix & disclosure** — once a fix is ready we will publish a patched release
  and a security advisory, and coordinate a disclosure timeline with you.

These are targets, not guarantees, for a volunteer-maintained project; we will
keep you updated on progress.

## Supported versions

The project is **pre-1.0** and under active development. Security fixes are
applied to the latest release / `main` branch only. There is no long-term
support branch yet; once `v1.0.0` is tagged this policy will be updated to list
supported version ranges.

| Version       | Supported          |
| ------------- | ------------------ |
| `main` (HEAD) | :white_check_mark: |
| pre-1.0 tags  | latest only        |

## Scope

This policy covers the code in this repository (the core, the adapters, the
default validator, and the generator tooling). Vulnerabilities in third-party
dependencies should be reported upstream; if a dependency issue affects oapi
users, let us know so we can bump the dependency.
