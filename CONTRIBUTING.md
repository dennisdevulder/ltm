# Contributing to ltm

ltm is a small protocol with a small reference implementation. Most
contributions fall into one of three buckets: fixing a bug in the reference
implementation, refining the spec, or porting the protocol to a new language.

## Ground rules

- The [protocol is the product](./SPEC.md). Behavior changes that can't be
  explained by a line in SPEC.md are almost always bugs in the spec, the
  implementation, or both — open an issue first so we can decide which.
- The reference implementation lives in Go. Pull requests must pass
  `go vet ./...`, `go build ./...`, and `go test ./...`, which is what CI
  runs. Please run them locally before you submit.
- No new direct dependencies without discussion. The binary size and the
  "fits on a $5 VPS" claim are load-bearing.
- Prose matters. The README, the SPEC, and the website are held to roughly
  the same bar as the code. Short sentences, concrete examples, no filler.

## Bugs and small fixes

[Open an issue](https://github.com/dennisdevulder/ltm/issues) first if the
fix isn't obviously a one-liner. For anything touching the wire format or
the JSON schema, the issue is where we agree on the desired behavior before
anyone writes a patch.

Good bug reports include:

- The version of `ltm` you're running (`ltm --version`).
- The exact command and its output.
- For packet-validation issues: the packet, with secrets redacted.

## Spec changes

SPEC.md is the source of truth. If you want to propose a field, a semantic
change, or a new verb:

1. Open an issue with the motivation, the concrete change, and what breaks.
2. If the change is accepted, update SPEC.md, the JSON Schema under
   `schema/`, and the Go validator in `internal/packet/` in the same PR.
3. Add or update tests that exercise the new behavior.

Spec changes that are not backwards-compatible bump the major version
(currently `v0.x`). We prefer additive changes during the pre-alpha window.

## Porting ltm to a new language

This is the contribution we're most interested in. If you want to write
a Rust client, a TypeScript server, a Python SDK — please do. We don't yet
have a portable conformance suite, so use the following as your substitute:

- **SPEC.md** enumerates every required and recommended field with its
  semantics. Read it end-to-end first.
- **`schema/core-memory.v0.2.json`** is the JSON Schema your validator
  should match. If it rejects a packet the reference server accepts, or
  vice versa, that's a bug in one of us.
- **Go tests under `internal/packet/` and `internal/cli/`** are the closest
  thing we have to behavioral reference vectors today. Mirror their shape.
- **HTTP API**: `POST /v1/packets` (body = packet JSON, returns `{"id": …}`),
  `GET /v1/packets?limit=N`, `GET /v1/packets/{id}`, `DELETE /v1/packets/{id}`,
  `GET /v1/healthz`. Bearer-token auth on all packet routes. Max packet size
  32 KB. That's the whole wire protocol.
- **Redaction pre-flight**: any second client MUST implement it before the
  first network call. Patterns are enumerated in the
  [README](./README.md#packets-travel-secrets-dont). We'd rather no second
  implementation than one that leaks secrets by default.

When your port runs, open a PR against the `README.md` "Further reading"
section adding a link to it — or open an issue and we'll add it ourselves.
A portable conformance test suite (JSON test vectors + a runner) is on the
roadmap; if you want to pair on that, say so.

## Setting up a development environment

```bash
git clone https://github.com/dennisdevulder/ltm
cd ltm
go build ./...
go test ./...
```

The Go toolchain version pinned in CI is the one we test against. Anything
newer should also work.

## Pull requests

- Keep PRs focused. One concern per PR makes review tractable.
- Write the commit message body, not just the subject. Explain why.
- If your change is user-visible, update the README and/or the website copy
  under `website/`.
- Sign off is not required.

## Security issues

See [SECURITY.md](./SECURITY.md). TL;DR: use GitHub's private security
advisory flow for anything sensitive; regular issues for everything else.

## Code of conduct

Be kind. Be direct. Assume good faith. If someone is being a jerk in an
issue or PR, flag it — we'd rather lose a contribution than lose contributors.
