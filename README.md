# product-site-template

A reusable Astro + Starlight foundation for product marketing sites: a
landing page, optional docs section, and optional changelog. Built by
extracting and generalizing netscli.com's site so future products (xtctx,
stasisdb.com, whatever's next) start from one clean, reviewed base instead
of forking and diverging.

netscli's real content ships in this repo as the reference example. Its
copy, images, and docs markdown are exactly what a new product replaces —
everything else is meant to stay as-is.

## What's generic vs. product-specific

Retheming for a new product means editing **only** these, nothing else:

- `src/data/site-content/*.ts` — all copy, branding colors, hero content,
  install commands, FAQ, social links, module toggles. This is the single
  source of truth; `src/data/site.ts` just assembles it into the `site`
  object every component reads from.
- `src/content/docs/**/*.md` — docs pages (Starlight-powered), only present
  if `modules.docs` is on.
- `CHANGELOG.md` at the repo root — optional local fallback for the
  changelog page; the page also fetches from the GitHub Releases API.
- `public/assets/*` — wordmark, favicon, hero screenshot, OG image.

Everything under `src/components/`, `src/layouts/`, `src/pages/`,
`src/scripts/`, and `src/styles/` is the shared shell and should not need
per-product edits. If you find yourself editing one of those to change
copy or branding, that value is probably missing from `site-content/` —
add it there instead.

## Layout

```
├── astro.config.mjs          — site URL, Starlight setup (gated on modules.docs)
├── package.json
├── CHANGELOG.md               — optional local changelog fallback
├── scripts/check-file-size.mjs — CI maintainability guard
├── scripts/a11y.mjs           — CI accessibility sweep
├── src/
│   ├── data/
│   │   ├── site.ts            — assembles site-content/* into `site`
│   │   └── site-content/*.ts  — all product-specific content (edit here)
│   ├── content/docs/          — Starlight docs pages (if modules.docs)
│   ├── styles/                — global.css (landing) + starlight/*.css (docs)
│   ├── layouts/Page.astro     — meta, OG, JSON-LD, CSS token injection
│   ├── components/*.astro     — Nav, Hero, Surfaces, Install, Faq, Footer
│   └── pages/                 — index, changelog, 404
└── dist/                      — build output (gitignored)
```

## Local development

```bash
npm install
npm run dev        # http://localhost:4321 with HMR
npm run build      # static output into dist/
npm run preview    # serve dist/
npm run check      # astro typecheck
npm run test:a11y  # build + axe-core accessibility sweep
```

Node 22+ is required. Astro 6.

## Module toggles

`src/data/site-content/modules.ts`:

```ts
export const modules: Modules = { docs: true, changelog: true };
```

- `docs: false` drops all `/docs/` nav/footer/404 links and in-copy "Full
  docs →" references. It does **not** by itself stop the Starlight build
  step — `astro.config.mjs` gates the `starlight()` integration on the same
  flag, but if you're removing docs from a product for good, also delete
  `src/content/docs/`, `content.config.ts`, `src/styles/starlight/`, and
  `src/components/starlight/`.
- `changelog: false` drops `/changelog/` nav/footer links. The
  `src/pages/changelog.astro` page itself isn't deleted by the flag; if a
  product has no changelog to speak of, remove the page directly.

## Theming

Brand colors live in `site-content/meta.ts`'s `branding` export
(`accent`, `accentAlt`, `accentHover`, `bg`, `fg`, `accentGradient`,
`wordmark`) and get threaded into real CSS custom properties
(`--accent`, `--accent-alt`, `--accent-hover`, `--bg`, `--fg`) via
`src/layouts/Page.astro`. Components reference `var(--accent)` etc. —
retheme by editing the `branding` fields, not by hunting for hex literals
across components.

Starlight's docs-side theming is a separate, already-tokenized system
(`src/styles/starlight/01-tokens-and-header.css`'s `--sl-color-accent` and
friends) — its internal custom-property and class names still carry a
`netscli-docs-*` prefix left over from the source extraction. They're
functionally generic (just oddly named) and weren't renamed in this pass;
worth a cosmetic sweep later, not a correctness issue.

## Hero downloads (optional)

If a product ships a downloadable installer, set `hero.downloads` (an
array of `{ label, url, hint? }`) and `hero.downloadsLabel` in
`site-content/hero.ts` — the hero renders a primary download button plus a
per-platform dropdown, and `src/scripts/landing-page.ts` swaps the primary
button to the visitor's detected OS at runtime, sourced from that same
array. Omit `hero.downloads` entirely for a hosted/SaaS product with no
installer; the hero then shows only the quick-install command.
`hero.packageManagerInstall` is a single optional one-liner (e.g. a
`winget`/`brew` command) shown as a static secondary line — it isn't
swapped per-OS.

## Live social proof

The hero's star/download counts and the changelog's release history both
read from the public GitHub REST API at runtime (`site.social.repo`
drives the repo slug), with no backend. Unauthenticated calls are rate-
limited to 60/hour per IP; on failure these numbers hide gracefully rather
than showing a stale or zero value.

## Versioning

`site-content/version.ts` exports a plain `productVersion` string, bumped
by hand per release. There's no cross-repo file to read it from once a
product's site lives in its own repo/monorepo directory — intentionally
simple rather than clever.

## Analytics

`site-content/meta.ts` → `analytics.cloudflareToken` controls the
Cloudflare Web Analytics beacon in `Page.astro`. Set to a string token to
enable, or omit to disable. Cloudflare Web Analytics is free with
unlimited pageviews and needs no consent banner in most jurisdictions.

## CI

`.github/workflows/ci.yml` runs the file-size maintainability guard
(`scripts/check-file-size.mjs`, 300-line cap on `.css`/`.mjs`/`.ts`/`.tsx`
with a couple of named exceptions), `astro build`, `astro check`, and the
axe-core accessibility sweep (`npm run test:a11y`). There is deliberately
no deploy workflow here — this repo is never deployed directly. Each
product's own repo gets its own `pages.yml` (or equivalent), gated to
`workflow_dispatch` rather than auto-deploy-on-push to `main` — netscli got
burned once by an unrelated merge silently pushing an in-progress redesign
live; don't repeat that.

## Known gaps

- `astro check` currently reports ~43 pre-existing TypeScript errors
  (implicit `any`s, a couple of DOM-typing gaps) that existed before this
  extraction — carried over as-is, not introduced by generalizing. Worth
  cleaning up, not blocking.
- Starlight's internal CSS custom-property and JS class/data-attribute
  names (`--netscli-docs-shell-gutter`, `.netscli-mobile-toc-wrapper`,
  etc. in `src/scripts/docs-header.ts` and `styles/starlight/*.css`) still
  carry a `netscli` prefix. They're private implementation details with no
  visible-string leakage, so left alone for this pass.

## Using this for a new product

1. Copy this repo's contents into the new product's `site/` directory (or
   use it directly if the product gets its own repo).
2. Edit every file under `src/data/site-content/` for the new product's
   name, copy, branding, install commands, FAQ, social links, and module
   toggles.
3. Replace images in `public/assets/` (wordmark, hero screenshot, OG
   image, favicon).
4. If `modules.docs` is on, replace `src/content/docs/**/*.md` with the
   new product's docs.
5. Add a `pages.yml` deploy workflow to the product's own repo, gated to
   `workflow_dispatch` (see "CI" above) — this template repo doesn't ship
   one.
