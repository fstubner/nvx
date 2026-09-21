---
name: product-site-customise
description: Edit a product site built from product-site-template — headline, install commands, feature cards, FAQ, docs, comparison table. Use whenever changing what a product site SAYS, on "update the landing page", "change the hero copy", "add an FAQ entry", or before editing anything under site/.
---

# Editing a product site's content

`AGENTS.md` at the site root is the full guide, and it is worth reading before
the first edit: it has the order to work in, what proves the site works, and
what not to touch. This skill exists so that guide gets read at all, and to
hold the one rule that keeps the whole arrangement working.

## The rule

**Edit `src/data/site-content/`. Do not edit components.**

Every section reads its own content file. The shell around them is shared with
the template and every other product built from it, so:

- A component edited here **takes this site out of the sync.** Template fixes
  then arrive as conflicts, and this site's own improvements cannot go back.
- A content file edited here is exactly what the arrangement expects. Pure
  content files are marked `merge=ours`, so they never conflict with the
  template's samples.

If a component cannot express what the product needs, that is a template
change, not a local one. See `product-site-upstream`.

## Order

1. `meta.ts` — name, domain, title, description, share image. Everything else
   derives the product name from here, so it goes first.
2. `hero.ts` — headline, subhead, the two commands, the installer menu.
3. `install.ts`, `surfaces.ts`, `faq.ts`, `footer.ts`.
4. `sections.ts` — which sections render, in what order. Dropping one is a
   real option: a product with no install story should drop `install` rather
   than fill it with something weak.

## Done is a command, not a judgment

```bash
npm run check:content
```

It lists what is still the template's sample and exits 0 when nothing is.
"It builds" is not done — it built before you started.

Then `npm run check` (0 errors) and `npm run build` (exit 0).

## Ask rather than invent

If you have not been told what the product is, who it is for, which install
routes actually exist per platform, the repo and domain, or whether it has
docs — ask. Copy written from a guess reads like copy written from a guess,
and it is the part of a site nobody goes back and rewrites.

## Match the voice

Short sentences. No "powerful", no "seamless", no exclamation marks, no claim
the product cannot back. Section leads say what the section is for rather than
selling it. A comparison table states what it measures and when it was
checked — a reader who catches one wrong row stops believing the whole page.
