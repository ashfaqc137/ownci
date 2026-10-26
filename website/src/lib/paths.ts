// The site is served under a base path (astro.config.mjs `base`, e.g. /ownci on sparshlabs.com).
// Write internal links as site paths ('/docs/') and pass them through href(); compare the current
// page with routePath() so the active-nav checks don't depend on the base.
const base = import.meta.env.BASE_URL.replace(/\/$/, '')

/** '/docs/' -> '/ownci/docs/' */
export function href(path: string): string {
  return `${base}${path}`
}

/** '/ownci/docs/' -> '/docs/' */
export function routePath(url: URL): string {
  const path = url.pathname.startsWith(base) ? url.pathname.slice(base.length) : url.pathname
  return path || '/'
}
