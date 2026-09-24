---
name: product-site-upstream
description: Decide whether a change made in a product's site/ belongs in the product-site-template, and take it up if it does. Use after building something in a product's landing page or docs that other products would want, or on "should this go upstream", "upstream this to the template", "is this generic".
---

# Taking work up to the template

The template is where the next product site starts. Anything generic that
stays in one product is work the next site does not get, and work a second
product will eventually write again — differently.

The expensive failure is not forgetting to upstream. It is upstreaming
something product-specific, because that lands in every future site and is
much harder to remove than to prevent.

## The test: is it generic?

Read the component, not the intent.

- **Does the file name the product?** Not in a comment — in a string, a URL, a
  class name, an import. `Compare.astro` mentions nothing; `Reach.astro`
  carries a comment saying it is nvx's own. The first went up, the second
  stays.
- **Does it read its content from a `site-content/` file**, the way every
  template section does, or does it have copy inside it?
- **Would a second product want it without editing the component?** If the
  answer needs "well, they'd change…", it is not ready — generalise first,
  upstream after.

Anything failing the test can still go up **after** being generalised. That is
usually the real work: see "Generalise, don't hardcode" below.

## Only upstream what has landed

Upstream from the product's `main`, never from an unmerged branch. Work still
in review can still change, and the template would then carry a version that
no longer exists downstream.

If a reviewer is holding a branch that contains something generic, note it and
come back — do not front-run the review.

## Generalise, don't hardcode

A product's version of a shared file usually has the product written into it.
The template's job is to take the *capability* and leave the specifics in
`site-content/`.

The pattern, from the crates.io download count (2026-09-21):

1. The product hardcoded its crate name in a fetch URL.
2. The template added an optional field to the relevant `site-content/` type.
3. The code takes it as a parameter.
4. **When it is absent, the feature does nothing** — no fetch, no request for
   `/crates/undefined` in every visitor's browser, no console error on a site
   that has nothing wrong with it.

Step 4 is the one that gets skipped. A generalisation that misbehaves when
unconfigured is worse than no generalisation, because every site that does not
use the feature now pays for it.

## Default new sections off

A section the template renders is a section every existing site suddenly has.
Add the name to the `LandingSection` union and the `COMPONENTS` map, add a
sample content file, and **leave it out of `sections.ts`**. An existing site
then builds byte-identically and a new one opts in by adding the name.

## Sample content is not the product's content

Replace it with something deliberately dull — `Example`, `Alternative A`. The
file's comment is where the judgment goes: what the section is for, where it
usually belongs in the order, and what makes it easy to get wrong.

Mark pure-content files `merge=ours` in `.gitattributes`, so a product editing
its own copy never conflicts with the sample. Only files that are **entirely**
content: anything holding shell code as well must keep merging normally, or a
sync silently drops the code half.

## Prove the thing renders

A typecheck passes on a section that renders nothing. Turn it on, build, assert
the markup, turn it back off:

```bash
# add the section to sections.ts
npm run build
grep -o 'id="<section>"' dist/index.html   # present
grep -c "<your row text>" dist/index.html  # every row
git checkout -- src/data/site-content/sections.ts
```

Then `npm run check`, `check:content`, `check:css`, `check:regions`.

## Taking it up

See `product-site-sync` for the mechanics and for the two silent failure modes
of the merge itself. Upstreaming a single component is usually a plain copy,
a branch and a PR rather than a subtree split — the split is for carrying a
product's whole site history back.
