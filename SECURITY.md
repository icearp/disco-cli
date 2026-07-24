# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately**. Do not open a public issue,
pull request, or discussion for a suspected vulnerability.

Email **dick.childress@icearp.net** with:

- a description of the issue and its impact,
- steps to reproduce (or a proof of concept),
- the `disco --version` output and the affected provider/OS.

You can expect an acknowledgement within a few business days. Once the issue is
confirmed and a fix is available, we'll coordinate disclosure timing with you.

## Supported versions

Fixes land on the `main` branch and ship in the next tagged release. The latest
tagged release and the current `main` tip are the supported targets; there is no
back-porting to older tags.

## Scope notes

- `disco` reads cloud state **read-only** — it discovers and records resources; it
  does not mutate cloud accounts.
- Secrets are scrubbed at the storage boundary (`store/sanitize.go`) before any
  resource attributes are written to the local database. If you find attribute
  data that should have been redacted but wasn't, that's an in-scope report.
- The local SQLite database and any `disco snapshot` archives may contain sensitive
  inventory data — treat them as you would any infrastructure inventory export.
