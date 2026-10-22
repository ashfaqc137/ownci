# OwnCI website

The public site for OwnCI: what problem it solves, how it works, the security model, and the
docs. Astro, fully static, no client-side JavaScript.

```bash
cd website
npm ci
npm run dev        # http://localhost:4321
npm run build      # astro check + static build into dist/
npm run preview    # serve dist/
```

Node ≥ 22.23 (see `.nvmrc` and `engines`). Dependencies are pinned to exact versions.

## Layout

| Path | What |
| --- | --- |
| `src/pages/` | `index` (why OwnCI), `how-it-works`, `security`, `docs/*`, `404` |
| `src/layouts/BaseLayout.astro` | Header, nav, footer, font imports |
| `src/components/DocsShell.astro` | Docs sidebar layout; add new docs pages to its `groups` |
| `src/components/Code.astro` | Code block; pass snippets as a string prop (no escaping needed) |
| `src/styles/global.css` | All styles. Colors, fonts, radii are tokens on `:root` |
| `public/_headers` | Security headers (CSP etc.) served by Cloudflare |

## Design system

Shares the warm-dark base and type of the sparsh site, with its own accent:

- **Accent** `--accent #6ee7d8` (signal cyan), used solid for primary buttons and the logo, and as
  tints (`--accent-08/14/25/40`) for active nav, highlighted cards and callouts.
- **Base** `--bg #11120f`, surfaces `--surface-1..3`, text ramp `--text` → `--text-faint`,
  muted teal-grey `--emph` for the second line of headlines.
- **Status** `--pass`, `--warn`, `--fail`. **Paper** tokens for the light GitHub mock-up.
- **Type** Manrope (headings), DM Sans (body), DM Mono (labels, code), self-hosted via
  `@fontsource` so no visitor request goes to a third party.

Use tokens, not raw hex, in new styles.

## Content

Facts on the site come from the repository's `README.md`. When a command, flag or the trust
model changes, update the matching page in the same pull request.

## Security headers

`public/_headers` sets a strict Content-Security-Policy (`default-src 'none'`; styles, fonts and
images from `'self'` only; no scripts). This works because the site ships no JavaScript and
`astro.config.mjs` stops Astro from inlining stylesheets or assets. If you add a `<script>`,
inline `style=""` attributes, or a third-party resource, the CSP will block it; update the policy
deliberately rather than loosening it to `'unsafe-inline'`.

## Deployment (Cloudflare Workers static assets)

The site deploys as plain static assets; `wrangler.jsonc` declares `assets.directory` and has no
`main` worker script. In the Cloudflare dashboard (**Workers & Pages → Create → Import a
repository**):

| Setting | Value |
| --- | --- |
| Root directory | `website` |
| Build command | `npm ci && npm run build` |
| Deploy command | `npx wrangler deploy` |

No build-time secrets or environment variables are needed. npm 12+ blocks dependency install
scripts by default (`esbuild`, `workerd`); the build works without them.
