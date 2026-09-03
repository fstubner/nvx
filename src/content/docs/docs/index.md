---
title: Overview
description: What Example is, and where to go next in these docs.
---

Example is a small command-line tool for doing one thing. It runs on Windows, macOS and Linux, and it has no runtime to install alongside it.

These three pages are samples. They exist so a fresh clone of this template has a docs section that builds, renders every feature of the docs shell, and passes the link and contrast checks. Replace them with your own.

## Where to start

| If you want to | Go to |
| --- | --- |
| Install it | [Installation](/docs/install/) |
| Look up a command or a flag | [Commands](/docs/commands/) |

## How these pages are built

Docs pages are Markdown files under `src/content/docs/docs/`. The sidebar that lists them is **not** derived from the files — it is written out in `src/data/site-content/docs.ts`, and nothing warns you when the two disagree. Delete a page and its sidebar entry becomes a link to nothing.

Every page needs a `title` and a `description` in its frontmatter. The description is what search engines show and what `/llms.txt` lists, so write it for a reader who has not seen the page.
