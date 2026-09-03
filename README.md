# product-site-template

An Astro + Starlight site for a product: a landing page, optional docs,
optional changelog. It is netscli.com's site, kept in sync with it, with
the product-specific parts collected where a new product replaces them.

netscli's real content ships here as the reference example. Its copy,
images and docs pages are exactly what a new product replaces; everything
else is the shell.

## What a new product edits

- `src/data/site-content/*.ts` -- all copy, links, install commands, FAQ,
  navigation, the domain, and the `modules` toggles. `src/data/site.ts`
  assembles these into the `site` object every component reads.
- `src/content/docs/**/*.md` -- the docs pages, if `modules.docs` is on.
  Their sidebar, title, description and logo are `site-content/docs.ts`;
  nothing warns when an entry points at a page that no longer exists.
- `public/assets/*` -- wordmark, hero screenshot, favicon, OG image.
- `CHANGELOG.md` -- the changelog page's local fallback; it also reads
  GitHub Releases at runtime.
- `src/styles/tokens.css` -- the brand palette, both themes. Then
  `src/styles/docs/theme.css` for how Starlight's tokens map onto it. The
  guide is `src/styles/README.md`.

Two things netscli ships that most products delete: `src/pages/install.sh.ts`
and `install.ps1.ts`, the installer scripts it serves from its own domain.

## What the shell is

```
src/styles/tokens.css        shared palette and base ramp
src/styles/code-surface.css  the dark surface code sits on, both themes
src/styles/theme-control.css the light/dark/system control
src/styles/docs/             the docs shell, one file per region
src/styles/landing/          the landing page's character and base rules
src/components/*.astro       Nav, Hero, Surfaces, Install, Faq, Footer
src/components/starlight/    the Starlight overrides
src/layouts/Page.astro       meta, OG, JSON-LD, the stylesheet order
src/pages/                   index, 404, changelog, robots.txt
src/scripts/                 landing behaviour, docs behaviour, changelog
scripts/                     the checks CI runs, and two verification tools
```

Starlight's own CSS is inside `@layer`, so the docs files outrank it as
plain rules; the whole shell has five `!important` declarations, all in
`docs/code.css` for inline styles, and `scripts/css-regions.mjs` keeps it
that way.

## Module toggles

`src/data/site-content/modules.ts`:

```ts
export const modules: Modules = { docs: true, changelog: true };
```

`docs: false` removes the Starlight integration from the build and every
docs link from the nav, footer, 404 page and surfaces copy. Delete
`src/content/docs/`, `content.config.ts`, `src/styles/docs/` and
`src/components/starlight/` as well when a product will never have docs.
`changelog: false` removes the changelog links; delete
`src/pages/changelog.astro` and `src/scripts/changelog/` with it.

## Local development

```bash
npm install
npm run dev          # http://localhost:4321
npm run build        # static output into dist/
npm run check        # astro typecheck
```

Node 22 or later; `.nvmrc` pins the version CI uses.

## Checks

CI (`.github/workflows/ci.yml`) runs, all blocking:

| Check | What it catches |
| --- | --- |
| `node scripts/check-file-size.mjs` | a source file over 300 lines |
| `npm run check:css` | a CSS declaration a later file always overrides |
| `npm run check:regions` | an unregistered docs stylesheet, or `!important` |
| `npm run check:wordmark` | the wordmark's transparent margin drifting from its token |
| `npm run build` and `npm run check` | the build, and TypeScript |
| `npm run check:changelog` | a dated changelog heading with no matching tag |
| `npm run test:a11y` | axe-core violations in both themes |
| `npm run check:contrast` | rendered text below its contrast floor |

Two more are for changing the styles, run by hand because they need a
git ref or a browser session:

- `node scripts/css-equivalence.mjs --old origin/main --stack docs|landing`
  works out, for every element, property, width and state, which
  declaration wins before and after your change, and lists the
  differences. `capture-search` first, once, so the search dialog's DOM
  exists.
- `node scripts/visual-snapshot.mjs record` then `check` compares 240
  screenshots (every page plus the open search dialog, two themes, eight
  widths) against a baseline.

`src/styles/docs/README.md` has the workflow.

## Preview builds

Set `SITE_PREVIEW=1` on a build that is deployed somewhere other than the
real domain: every page gets `robots: noindex` and the analytics beacon is
left out, so a PR preview neither gets indexed nor reports into the real
property.

## What is still netscli-shaped

Measured, so a new product knows where to look:

- The reference content is netscli's: the docs pages, the FAQ, the install
  commands and the assets under `public/assets/` (including the wordmark the
  `check:wordmark` guard measures). Replace them; nothing else points at
  netscli once you do, except the two lines below.
- `astro.config.mjs` still hard-codes netscli's GitHub URL for the docs
  header link and netscli's wording for the `twitter:image:alt` tag. Both
  belong in `site-content/`; they are the next things to move.
- The landing components carry 37 literal colours, most of them rgba()
  greys; `src/styles/README.md` lists where.
- Deployment is the product's own: there is no `CNAME`, no Pages workflow.
  netscli deploys from its monorepo with its own workflows.

## Keeping it in sync

This tree is netscli's `site/` directory, and since 2026-09-03 the two
histories share a base, so a sync is an ordinary merge rather than a
hand-applied patch. In the netscli repo:

```
git subtree split --prefix=site -b site-split main
```

Then here, with netscli added as a remote:

```
git fetch netscli site-split:refs/remotes/netscli/site-split
git merge netscli/site-split
```

Conflicts appear only where a generalisation in this repo touches a line the
sync changed -- the last sync had three, all in `astro.config.mjs`,
`src/data/site.ts` and `src/data/site-content/types.ts`. Resolve by keeping
both sides: this repo's `modules` gating plus netscli's change.

Going the other way -- an improvement made in a product's site that belongs
in the template -- is a cherry-pick onto a branch here, then the same merge
into netscli. Improvements are easiest to land if they go into netscli
first, because that is the direction the merge runs.

A brand-new product should start with `git subtree add --prefix=site <this
repo> main`, so it shares this history from its first commit and both
directions are ordinary merges from then on.
