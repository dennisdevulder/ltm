# website — ltm-cli.dev

Static site for https://ltm-cli.dev.

## What's here

- `index.html` — the landing page (exported from the design agent).
- `install` — the `curl | sh` install script (a copy of `/install.sh` at the repo root). Served with no extension so `curl -fsSL https://ltm-cli.dev/install | sh` works.
- `CNAME` — tells GitHub Pages which custom domain to serve.

## Deploy

Deployed automatically by `.github/workflows/pages.yml` on every push to `main` that touches `website/`.

### One-time repo setup

1. Go to **Settings → Pages** on `github.com/dennisdevulder/ltm`.
2. Under **Source**, pick **GitHub Actions**.
3. The first push after that triggers the workflow and the site goes live at `https://dennisdevulder.github.io/ltm/` until DNS is wired.

### One-time DNS setup for `ltm-cli.dev`

At your DNS provider, add **four A records** on the apex (`@`):

```
185.199.108.153
185.199.109.153
185.199.110.153
185.199.111.153
```

Plus a **CNAME** on `www` → `dennisdevulder.github.io.`

Then in **Settings → Pages**, put `ltm-cli.dev` in the Custom domain box and wait for the "DNS check successful" tick. Enable **Enforce HTTPS**.

## Keeping `install` in sync

`website/install` is a copy of the canonical `install.sh` at the repo root. If you edit one, copy to the other, or replace with a symlink when deploying locally:

```bash
cp install.sh website/install
```

A pre-commit hook or GitHub Action can automate this if it becomes a pain.

## Editing copy

Every text string is in `index.html`. No JSX, no data files. If a fact in the page becomes stale, search the HTML for the old string and replace.
