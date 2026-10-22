import { defineConfig } from 'astro/config'

// Fully static, script-free site. Stylesheets and fonts are emitted as files (never inlined)
// so the Content-Security-Policy in public/_headers can stay at 'self' with no 'unsafe-inline'.
export default defineConfig({
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
