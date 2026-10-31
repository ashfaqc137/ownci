import { defineConfig } from 'astro/config'

// Fully static site; the only script is the copy button in Code.astro. Stylesheets, scripts
// and fonts are emitted as files (never inlined) so the Content-Security-Policy in _headers can
// stay at 'self' with no 'unsafe-inline'.
//
// Served at sparshlabs.com/ownci/ by its own Worker (wrangler.jsonc route). Workers static
// assets don't strip the path prefix, so pages are built into dist/ownci/ and wrangler serves
// dist/; links go through src/lib/paths.ts so they carry the base.
export default defineConfig({
  site: 'https://sparshlabs.com',
  base: '/ownci',
  outDir: './dist/ownci',
  trailingSlash: 'always',
  build: {
    inlineStylesheets: 'never',
  },
  vite: {
    build: {
      assetsInlineLimit: 0,
    },
  },
})
