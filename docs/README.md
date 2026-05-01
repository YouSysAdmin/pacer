# Pacer docs

Single Hugo site, one shared theme. No user/internal split — everything is in `content/`.

```
docs/
├── content/                ← markdown
│   ├── _index.md           home
│   ├── description/        what the tool is + pipeline + routing
│   └── installation/       github / aws / server
├── themes/runner-docs/     ← theme (dark, amber, terminal-aesthetic)
├── hugo.toml               site config
└── iam-role.json           IAM policy template, referenced from installation/aws.md
```

## Develop

```bash
cd docs && hugo server -D     # dev preview at localhost:1313
```

## Build

```bash
cd docs && hugo --minify      # → docs/public/
```

## Add a page

1. `mkdir content/<section>` and add `_index.md` with `title`, `description`, `weight`.
2. Add pages inside as `*.md`, each with its own `weight` for sidebar ordering.
3. Hide a section from the sidebar by setting `sidebar: false` in its `_index.md`.

## Add to top nav

Edit `[[menu.main]]` blocks in `hugo.toml`.

## Tweaking the theme

Theme params (set in `hugo.toml`):

| Param                   | Effect                                                               |
|-------------------------|----------------------------------------------------------------------|
| `brand` / `brand_sub`   | Wordmark + small badge next to it                                    |
| `tagline`               | Home page H1                                                         |
| `description`           | Meta description + home lead                                         |
| `cta_url` / `cta_label` | Top-right primary button (omit `cta_url` to hide)                    |
| `repoURL`               | Source repo (only shown in footer when `show_footer_project = true`) |
| `edit_base`             | "Edit this page" link in TOC partial; empty disables                 |
| `trust_badges`          | Array of strings rendered under the home hero CTAs                   |
| `terminal_lines`        | Array of `<span>`-marked lines for the home hero terminal            |
| `show_footer_project`   | Whether the footer renders the source/releases/issues column         |

Theme tokens live in `themes/runner-docs/assets/css/tokens.css` (dark, amber). Component CSS in
`themes/runner-docs/assets/css/docs.css`.
