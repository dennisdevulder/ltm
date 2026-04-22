# Security policy

## Reporting a vulnerability

Please use **[GitHub's private security advisory flow](https://github.com/dennisdevulder/ltm/security/advisories/new)**
for anything sensitive. This routes the report privately to the maintainers,
lets us collaborate on a fix in a private fork, and gives us a single place to
coordinate disclosure.

Non-sensitive bugs, feature requests, and spec ambiguities belong in
[issues](https://github.com/dennisdevulder/ltm/issues) — please use the regular
public tracker there.

## What to report

Examples of things worth a private advisory:

- A bypass or false-negative in the [redaction pre-flight](./README.md#packets-travel-secrets-dont)
  (a secret pattern we fail to catch; an encoding that slips through).
- A packet that the schema validator accepts but that crashes or misbehaves
  the reference server.
- Token-handling issues (credential leakage in logs, filesystem permissions
  on `~/.config/ltm/credentials`, clipboard exposure, etc.).
- Any path that lets a caller push a packet without a valid bearer token or
  an authenticated device flow.
- Anything in the OAuth device flow that doesn't follow RFC 8628.

Examples of things that are NOT security issues (open a regular issue
instead):

- Missing redaction patterns we haven't added yet (e.g. a provider key shape
  we've never seen). Please still tell us — it's just not a disclosure event.
- Rough edges in the CLI UX, the TUI picker, or error messages.
- Spec clarifications.

## Redaction pre-flight policy

ltm scans packets client-side before upload and refuses to push anything
containing absolute paths, AWS keys, GitHub tokens, JWTs, private-key
headers, Google API keys, Slack tokens, Stripe keys, or SSH public keys. The
full list lives in `internal/packet/redact.go` and is enumerated in the
[README](./README.md#packets-travel-secrets-dont).

This is a best-effort safety net, not a guarantee. We design for it to be
noisy rather than quiet: we'd rather block a legitimate push and make the
caller pass `--allow-unredacted` than silently leak a secret. If you find a
pattern we miss consistently, please open an issue with a redacted sample
so we can add a rule.

## Disclosure timeline

ltm is pre-alpha and maintained by a small group. We aim to:

1. Acknowledge a valid advisory within 7 days.
2. Ship a fix or a mitigation as quickly as the complexity allows.
3. Publish a GitHub Security Advisory crediting the reporter (unless they
   prefer to stay anonymous) after the fix ships.

We don't currently run a bug bounty.

## Supported versions

Only the `main` branch and the latest tagged release. Older tags receive fixes
only for issues that also affect `main`.
