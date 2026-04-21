# website — ltm-cli.dev

Static site for https://ltm-cli.dev. Hand-rolled HTML + inline CSS. No bundler, no build step.

## What's here

- `index.html` — the landing page (~18 KB, single file).
- `install` — the `curl | sh` install script (a copy of `/install.sh` at the repo root). Served with no extension so `curl -fsSL https://ltm-cli.dev/install | sh` works.

## Deploy

Any static host works. Two easy options:

**Cloudflare Pages**
1. Connect the repo at https://dash.cloudflare.com/.
2. Build command: *(none)*
3. Build output directory: `website`
4. Attach the `ltm-cli.dev` domain.

**GitHub Pages**
1. Settings → Pages → Source: `Deploy from a branch`
2. Branch: `main`, Folder: `/website`
3. Add a CNAME file pointing `ltm-cli.dev` at `<user>.github.io`.

## Keeping `install` in sync

`website/install` is a copy of the canonical `install.sh` at the repo root. If you edit one, copy to the other, or replace with a symlink when deploying locally:

```bash
cp install.sh website/install
```

A pre-commit hook or GitHub Action can automate this if it becomes a pain.

## Editing copy

Every text string is in `index.html`. No JSX, no data files. If a fact in the page becomes stale, search the HTML for the old string and replace.
