---
name: product-site-sync
description: Pull the product-site-template's changes down into a product's site/ subtree, or pull a product's changes into the template. Use when a site is behind the template, when template work needs to reach a product, or on "sync the site", "update from the template", "take the template's changes". Covers the merge, the conflicts that recur, and the two failures that are silent.
---

# Syncing a product site with the template

The template and each product's `site/` share history, so a sync is an ordinary
`git merge` rather than a hand-applied patch. What makes it worth a skill is
that **two of its failure modes produce no conflict and no error**, and one of
them silently breaks every future sync.

Read `README.md` → "Keeping it in sync" for the commands. This is what the
commands do not tell you.

## Before you start

```bash
git config merge.ours.driver true
```

Per clone, not per repo. `.gitattributes` marks pure-content files `merge=ours`
so a product's own copy survives; without the driver those come back as
ordinary conflicts with nothing to decide.

## Direction

- **Template → product**: in the product repo, `git subtree pull --prefix=site
  <template-remote> main`.
- **Product → template**: in the product repo, `git subtree split --prefix=site
  -b site-split main`, then in the template, fetch that branch and merge it.

## Never run `git stash` while the merge is uncommitted

This is the one that costs the most and looks harmless. Measured 2026-09-21,
mid-sync: `git stash --include-untracked` swallowed the conflict resolution
**and cleared `MERGE_HEAD`**. Committing after that produces an ordinary
commit instead of a merge commit — so the two histories stop sharing a base,
and every future sync degrades from a merge to a hand-applied patch. Nothing
warns you; the commit succeeds.

If you need a clean tree mid-merge, finish the merge first and amend after.

**Always confirm the result is a real merge:**

```bash
git log --oneline -1 --format='%h parents: %p'
```

Two parents, or the sync did not happen. Recovering means resetting and
redoing the merge, which is cheap — shipping the broken one is not.

## Check what arrived as a NEW file

`.gitattributes` protects files that already exist. It cannot protect against
a file that does not. A product's own screenshots, docs pages and assets
arrive as additions, which git merges **without a conflict**, and they land in
the template unnoticed.

```bash
git diff --cached --diff-filter=A --name-only main
```

Read that list and ask of each: is this generic, or is it one product's? In
the 2026-09-21 sync, four `gui-*.png` product screenshots came through this
way while six product doc pages and two other screenshots were caught as
conflicts, because those had been deliberately deleted before.

## Check whether the sync un-did a generalisation

The template exists to parameterise what a product hardcodes. A sync takes the
product's version of a file whole, so a file the template generalised comes
back specific — again with no conflict, because the product only changed lines
the template had also changed.

Measured in the same sync: `src/scripts/landing-page.ts` had been parameterised
to take a repo slug; netscli's version added a crates.io fetch with its own
crate name written into the URL. The merge kept netscli's file. Nothing failed.

**Resolve by keeping both sides** — the template's parameter plus the
product's feature. That sync added an optional `cratesIoCrate` to
`SocialProof`, so the feature is available to every product and hardcoded for
none.

Files where this recurs, because they hold shell code as well as content:
`astro.config.mjs`, `src/data/site.ts`, `src/data/site-content/types.ts`.

## Conflicts that are not really conflicts

`modify/delete` on a product's own docs pages and screenshots means the
template deleted them on purpose. Keep them deleted:

```bash
git rm --force <path>...
```

The template's sample docs are `commands`, `index` and `install`. Anything else
under `src/content/docs/docs/` belongs to a product.

## Prove it before committing

```bash
npm run check          # 0 errors
npm run build          # exit 0
npm run check:content  # what is still sample content
```

`check:contrast` and `test:a11y` drive a browser and may fail to start on a
machine that blocks downloaded binaries from executing — `Access is denied
(os error 5)` from `selenium-manager.exe` is that, not a regression. Say so
rather than claiming they passed.
