# OwnCI website

The public site for OwnCI: what problem it solves, how it works, the security model, and the
docs. Astro, fully static; the only client-side JavaScript is the code blocks' copy button.
Served at [sparshlabs.com/ownci/](https://sparshlabs.com/ownci/) as one of the
[sparshlabs](https://github.com/ashfaqc137/sparshlabs) project sites.

```bash
cd website
npm ci
npm run dev        # http://localhost:4321/ownci/
npm run build      # astro check + static build into dist/ownci/, plus dist/_headers
npm run preview    # serve the build at http://localhost:4321/ownci/
```

Node 22 or newer (see `.nvmrc` and `engines`). Dependencies are pinned to exact versions.

## Layout

| Path | What |
| --- | --- |
| `src/pages/` | `index` (why OwnCI), `how-it-works`, `security`, `docs/*`, `404` |
| `src/layouts/BaseLayout.astro` | Header, nav, footer, font imports |
| `src/components/DocsShell.astro` | Docs sidebar layout; add new docs pages to its `groups` |
| `src/components/DocsHeading.astro` | Docs `h2`/`h3` with a `#` permalink; use it for every heading in `src/pages/docs/`. An `h2` takes its `<section>`'s id, an `h3` gets its own |
| `src/components/Code.astro` | Code block with a copy button; pass snippets as a string prop (write `${{` as `\${{` in template literals) |
| `src/lib/paths.ts` | `href()` and `routePath()`: every internal link goes through `href('/docs/')` so it carries the `/ownci` base |
| `src/styles/sparshlabs.css` | Shared sparshlabs theme: tokens, header, footer, sections, docs, code. **A synced copy, don't edit it here** |
| `src/styles/global.css` | OwnCI-only styles (hero visual, compare, flow, command strip), using the theme's tokens |
| `_headers` | Security headers (CSP etc.) served by Cloudflare, copied to `dist/` by `npm run build` |

## Design system

The tokens and the site shell (header, nav, footer, heroes, cards, docs layout, code blocks,
tables) come from the shared sparshlabs theme, so OwnCI, sparsh and sparshlabs.com look like one
family. `src/styles/sparshlabs.css` is a copy of `theme/sparshlabs.css` in the
[sparshlabs repo](https://github.com/ashfaqc137/sparshlabs): change the theme there and run
`npm run sync-theme` in that repo, which rewrites this copy.

- **Accent** `--accent` (sparshlabs signal cyan), solid for primary buttons and the logo, and as tints
  (`--accent-08/14/25/40`) for active nav, highlighted cards and callouts.
- **Base** `--bg #11120f`, surfaces `--surface-1..4`, text ramp `--text` → `--text-faint`,
  `--emph` for the second line of headlines.
- **Status** `--pass`, `--warn`, `--fail`. **Paper** tokens for the light GitHub mock-up.
- **Type** Manrope (headings), DM Sans (body), DM Mono (labels, code), self-hosted via
  `@fontsource` so no visitor request goes to a third party.

Use tokens, not raw hex, in new styles. The header reads `sparshlabs / OwnCI`: the `sparshlabs`
link goes to `/`, the lab's home page, which a different Worker serves.

## Content

Facts on the site come from the repository's `README.md`. When a command, flag or the trust
model changes, update the matching page in the same pull request.

## Security headers

`_headers` sets a strict Content-Security-Policy (`default-src 'none'`; scripts, styles, fonts
and images from `'self'` only). This works because `astro.config.mjs` stops Astro from inlining
stylesheets, scripts or assets, so the one script (the copy button in `Code.astro`) ships as a
file under `/_astro/`. Inline scripts, inline `style=""` attributes, or a third-party resource
will be blocked by the CSP; update the policy deliberately rather than loosening it to
`'unsafe-inline'`.

## Deployment (Cloudflare Workers static assets)

The site deploys as plain static assets; `wrangler.jsonc` declares `assets.directory` and has no
`main` worker script. It is served at `sparshlabs.com/ownci/` through the route in
`wrangler.jsonc` (`sparshlabs.com/ownci*`). Workers static assets don't strip a path prefix, so
the pages are built into `dist/ownci/` and wrangler serves `dist/`; `_headers` must sit at the
root of that directory, which is why it lives outside `public/` and its patterns start with
`/ownci/`. The root of sparshlabs.com, including `robots.txt`, is served by the sparshlabs
Worker. In the Cloudflare dashboard (**Workers & Pages → Create → Import a
repository**):

| Setting | Value |
| --- | --- |
| Root directory | `website` |
| Build command | `npm ci && npm run build` |
| Deploy command | `npx wrangler deploy` |

No build-time secrets or environment variables are needed. npm 12+ blocks dependency install
scripts by default (`esbuild`, `workerd`); the build works without them. On a `*.workers.dev`
or preview URL the site is at `/ownci/` (the bare root returns 404), because that is where the
build puts it.
