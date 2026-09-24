---
name: product-site-spin-off
description: Start a new product's landing page and docs from the product-site-template, as a subtree inside the product's own repo. Use on "add a site to this project", "set up a landing page", "spin off a product site", or when a product has no site/ directory yet.
---

# Starting a product site from the template

Add it as a **subtree**, not a copy. A copy is a site that can never take a
fix from the template or give one back; a subtree shares history, so syncing
works in both directions from the first commit.

```bash
git subtree add --prefix=site https://github.com/fstubner/product-site-template main
```

Run this from the product's repo root.

## Then four things, because the template's root is a site and a subtree is not

The template repo's root **is** the site. Inside a product it becomes a
subdirectory, and four things assume otherwise. All were found by doing this,
not by reading it.

1. **Move the CI workflow to the project root.** It arrives at
   `site/.github/workflows/ci.yml`, and GitHub only reads `.github/` at the
   repo root — so a project that leaves it there has CI that **silently does
   not exist**. Move it to `.github/workflows/`, add `defaults: run:
   working-directory: site`, and point the file-size guard step at
   `site/scripts/check-file-size.mjs`.

2. **Decide which CHANGELOG the site reads.** `src/pages/changelog.astro`
   imports `../../CHANGELOG.md` — the site directory's own copy. A product's
   changelog usually lives at the project root: change the import to
   `../../../CHANGELOG.md` and delete `site/CHANGELOG.md`, or keep the site's
   copy deliberately. `scripts/changelog-dates.mjs` needs the same decision.

3. **Move `AGENTS.md` up, or leave a pointer.** It arrives at
   `site/AGENTS.md`. An agent working in `site/` finds it; one working from
   the project root does not.

4. **Move `.claude/skills/` up, or the skills do not exist.** Same reason as
   the CI workflow, and it fails the same silent way: Claude Code reads
   `.claude/` at the **repo root**. Measured 2026-09-21 by subtree-adding this
   template into a scratch repo — `.claude/` arrived at `site/.claude/`,
   alongside `site/AGENTS.md` and `site/.github/workflows/ci.yml`, where
   nothing looks for any of them. Move the directory to the project root, or
   symlink it, then confirm `product-site-customise` and `product-site-sync`
   are listed.

Everything else works unchanged from inside a subtree: the build, all four
static guards, `check:content`, the changelog check and the two browser
sweeps.

## Then fill it in

That is `product-site-customise`, and `AGENTS.md` has the order. The short
version: content only, `meta.ts` first, and `npm run check:content` is the
definition of done — not "it builds", which it did before you started.

## Do not customise by editing components

The whole arrangement depends on the shell staying shared. A product that
edits `Hero.astro` instead of `hero.ts` has taken itself out of the sync: every
future template fix arrives as a conflict, and its own improvements cannot go
back. If a component genuinely cannot express what the product needs, that is
a template change — see `product-site-upstream`.

## Deployment is the product's own

There is no `CNAME` and no Pages workflow in the template. Add them in the
product repo, alongside the CI workflow you just moved.
