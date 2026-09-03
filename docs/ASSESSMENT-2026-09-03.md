# Engineering assessment, 2026-09-03

Of this repository at the sync to netscli@3f79335 plus the generalisation
commit on top. Written to answer one question honestly: is this a template
with engineering rigour, or netscli's site with a README? Every finding
cites what was measured; what was not examined is listed at the end.

## Status, later the same day

This is a snapshot, and the tree moved under it within hours. What closed:

- **#4, template fitness (the shell is not brand-neutral).** Closed. The
  `netscli-` prefix became `ui-` across 79 tokens, six class names and the
  docs table marker; the product name left the 404 page, the changelog page,
  the two nav labels, `/llms.txt` and the type docs; and the comments that
  used netscli strings as their measured example now state the measurement
  without the name. Nothing outside the sample content, the docs pages and
  the images names netscli.
- **#8's evidence line is stale**: the landing scrollbar rule reads
  `var(--ui-bg)` now. The duplication itself is still there.

What is new since, and not assessed here: the sample content for a fictional
product, `AGENTS.md`, `npm run check:content`, `npm run theme`, the
composable landing sections and the second layout, and CI on a self-hosted
runner. The theme tool in particular changes finding #5's weight -- a
re-brand no longer means editing components for the accent, though the 37
literal colours are still there for everything else.

Everything else below stands.

## Scope

In scope: everything under `src/`, `scripts/`, `astro.config.mjs`,
`package.json`, `.github/`. Depth: every file under `src/scripts/`,
`src/styles/`, `src/data/`, `src/layouts/`, `src/pages/` and `scripts/`
was read; `src/components/*.astro` were read for structure and styling, not
line by line; `src/content/docs/` (netscli's docs) was not read.

Out of scope: deployment (there is none here), the netscli monorepo's own
CI, and the content's accuracy.

## Environment

Astro 7.2.9, Starlight 0.41.9, Node 22 (`.nvmrc`), TypeScript on
`astro/tsconfigs/strict`, no test runner, no linter or formatter
configured. Checks run: `astro build` (14 routes), `astro check` (66 files,
0 errors, 0 warnings, 0 hints), `check-file-size`, `css-shadowing --max 0`,
`css-regions`, `wordmark-inset`, `changelog-dates`, `contrast-sweep`,
`a11y` (axe), `npm audit --omit=dev` (0 vulnerabilities), this suite's
`check-smells` and `check-cochange` (84 commits of site history: no
shotgun-surgery pattern).

## Findings

| # | Severity | Area | Finding | Evidence | Recommendation |
|---|---|---|---|---|---|
| 1 | High | Maintainability | The changelog renderer, markdown parser and OS detection have no tests; the only checks are runtime sweeps of the built pages. | No test runner in `package.json`; `src/scripts/changelog/markdown-block.ts` (240 lines) and `markdown-inline.ts` (187) parse untrusted-shaped input with zero unit coverage. | Add a test runner (Vitest is already transitively present via Astro) and cover the two markdown modules and `landing/os-tabs.ts` first; they are pure functions and the highest-churn logic. |
| 2 | Medium | Maintainability | The 300-line file guard does not see `.astro` files, so the four largest source files are exempt from it. | `scripts/check-file-size.mjs:17` lists `.css .mjs .rs .ts .tsx`; `Hero.astro` 615, `changelog.astro` 440, `Nav.astro` 424, `Install.astro` 402 lines. | Add `.astro` to the extension set with transition exceptions for these four, then split each: the markup is under a third of every one; the rest is CSS that belongs beside `styles/landing/`. |
| 3 | Medium | Architecture | Every component's `<style>` is `is:global`, so any component can style any other's markup and nothing stops it. | All six landing components declare `<style is:global>`; one cross-component rule was found and moved during the tier split (`.surface-visual img` in Hero). | Either scope the styles (drop `is:global` and use `:global()` only where a child's markup must be reached) or move component CSS into `styles/landing/*.css` files named per component, where the equivalence checker covers them. The second is the smaller change and makes the landing tier fully checkable. |
| 4 | Medium | Template fitness | The shell is not brand-neutral: 41 of the shell's own files mention netscli, and every token and class carries the prefix. | `grep -rli netscli src/components src/layouts src/scripts src/styles src/pages` = 41; 79 `--netscli-*` tokens, `.netscli-*` classes, `data-netscli-table`. | One mechanical rename (`netscli-` to a neutral prefix) verified by the screenshot check, then a guard that fails on the word in shell files. Content files keep it. |
| 5 | Medium | Template fitness | The landing components carry 37 literal colours, so a re-theme beyond the accent means editing components. | Install 14, Hero 11, Nav 8, Surfaces 4 (comments stripped); listed in `src/styles/README.md`. | Promote the ones that recur (the `#1ec88c` hover, the `#252b36` raised surface, the `rgba(0,0,0,.x)` shadows) to `landing/tokens.css`; leave the one-offs with a comment. |
| 6 | Low | Correctness | Content strings are injected as HTML in ten places, with no escaping or schema check, so a typo in `site-content/*.ts` renders raw. | `grep -rn set:html src` = 10; the strings live in `site-content/faq.ts`, `surfaces.ts`, `install.ts`. | Acceptable while content is authored in the repo by its owner. If the template is meant for others, validate the content module at build time (a `zod` schema in `content.config.ts`, which Starlight already uses for docs). |
| 7 | Low | Maintainability | Two verification scripts break this suite's own smell thresholds. | `check-smells`: `scripts/visual-snapshot.mjs` 558 lines; `scripts/css-equivalence.mjs` nesting depth 7 at line 91. Both sit in `scripts/`, outside the file-size guard's `src` root. | Split `visual-snapshot.mjs` into capture and compare halves (its own comments already name the seam); flatten the index loop in `css-equivalence.mjs`. Widen the guard root to `scripts/` once done. |
| 8 | Low | Maintainability | The scrollbar rules are declared twice with different track colours. | `styles/docs/base.css` and `styles/landing/base.css` each carry the nine `::-webkit-scrollbar` rules; docs uses `#151515`, landing `var(--netscli-bg)`. | Decide one, put it in a shared `scrollbars.css`, and prove it with the screenshot check. |
| 9 | Low | Tooling | No linter or formatter, so style is by convention only. | No `eslint`, `prettier`, `biome` or `.editorconfig` at the root; mixed CRLF/LF already shows in git's warnings on every commit. | Add Prettier with a check in CI and a `.gitattributes` fixing line endings. Cheap, and it removes a class of noise from every review. |
| 10 | Info | Reliability | Runtime GitHub API calls degrade by hiding the element on failure. | `landing-page.ts:55-79`, `changelog-page.ts:29`: `r.ok ? r.json() : null`, `.catch(() => {})`, elements stay `hidden`. | Fine as designed; the unauthenticated 60/hour limit is documented in the README. |
| 11 | Info | Reliability | Every empty `catch` block is a deliberate localStorage fallback with a comment saying so. | `landing/os-tabs.ts`, `theme-select.ts`, `docs-header-disposal.ts`. | None. |
| 12 | Info | Security | Zero known vulnerabilities in production dependencies; strict TypeScript; no `any`, `@ts-ignore` or `as unknown` in the scripts. | `npm audit --omit=dev`; grep over `src/scripts`. | None. |

## Unconfirmed / requires investigation

- Whether the docs pages differ between Starlight 0.41.7 and 0.41.9. The
  screenshot baseline used to prove the CSS migration was recorded with
  0.41.7 installed locally while the lockfile said 0.41.9; CI built and
  passed a11y and contrast on 0.41.9, and a screenshot comparison on the
  lockfile versions was started as this was written.
- Whether `modules.docs = false` builds cleanly. The flag gates the
  integration and the links; it has not been exercised. The trial
  instantiation is the test.

## Summary

**Strengths.** The CSS is the strongest part: two tiers with a documented
seam, no load-order dependence, five justified `!important` declarations,
guards that keep it so, and two independent ways to prove a change renders
the same (`css-equivalence.mjs`, `visual-snapshot.mjs`). The content layer
is real: copy, links and toggles live in `site-content/`, and the
components read from it. CI blocks on every check, including rendered
contrast and axe. Types are strict and clean.

**Key risks.** The logic has no tests (#1), the largest files are outside
the size guard (#2), and the component styles are global (#3). Together
these mean a change to a component is checked only by screenshots and by
eye. For a template that other products will edit, #4 and #5 are the
findings a new user meets first.

**Priority order.**
1. Tests for the markdown and OS-detection modules (#1).
2. `.astro` in the file-size guard, then split the four big components (#2).
3. Move component CSS into per-component files under `styles/landing/` so
   the equivalence checker covers it (#3).
4. The prefix rename with a guard (#4), then token promotion (#5).
5. Prettier and line endings (#9); the script smells (#7); scrollbars (#8).

**Coverage gaps.** Component markup was skimmed, not read; docs content
not read; hover, focus and the search dialog's empty states are outside
the screenshot set; no load or performance testing; no review of the
Starlight override components (`src/components/starlight/`) beyond their
styles; `modules.docs = false` not exercised.
