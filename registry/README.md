# Registry stubs

Name claims on package registries to prevent squatting. Each subdirectory is a minimal, legitimate package that reserves the name and points to the real repo.

These are **not** the actual ltm client libraries. If/when language SDKs ship, these names get upgraded with real content.

## What to publish and where

| Registry | Name | Directory | Publish command |
|---|---|---|---|
| PyPI | `ltm-cli` | [`pypi/`](./pypi/) | `cd registry/pypi && python -m build && twine upload dist/*` |
| crates.io | `ltm-cli` | [`cratesio/`](./cratesio/) | `cd registry/cratesio && cargo publish` |
| npm | `@ltm/cli` | [`npm/`](./npm/) | `cd registry/npm && npm publish --access public` |

## Before you publish

**PyPI** — requires a PyPI account with a valid API token in `~/.pypirc`.

```bash
python -m pip install --upgrade build twine
```

**crates.io** — log in with GitHub at https://crates.io and generate a token:

```bash
cargo login <token>
```

**npm** — create the `ltm` npm organization first (free):

```bash
npm login
npm org create ltm
# or, if using a scoped personal package, skip the org and
# publish as @<yourname>/ltm instead — less defensible but works
```

## After publishing

Each registry page should link back to https://github.com/dennisdevulder/ltm and https://ltm-cli.dev. Update the stubs' version when the real SDK eventually ships.
