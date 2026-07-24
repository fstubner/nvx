import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import { site } from './src/data/site';

// Canonical site URL. Used by Astro for absolute URL generation in
// sitemap/RSS integrations if we add them later, and surfaces in the
// generated HTML via the built-in `Astro.site` global.
export default defineConfig({
  site: site.meta.domain,
  integrations: [
    // Gated on modules.docs (site-content/modules.ts). Turning docs off for
    // a product means deleting this whole `starlight(...)` block (and
    // src/content/docs/, content.config.ts, src/styles/starlight/,
    // src/components/starlight/) — modules.docs alone only controls the
    // nav/footer/404 links and in-copy references, not this build step.
    ...(site.modules.docs
      ? [
          starlight({
            title: `${site.meta.siteName} docs`,
            description: site.meta.description,
            disable404Route: true,
            logo: {
              src: './public/assets/netscli-wordmark.png',
              replacesTitle: true,
            },
            components: {
              Header: './src/components/starlight/Header.astro',
              Footer: './src/components/starlight/Footer.astro',
              MobileMenuFooter: './src/components/starlight/MobileMenuFooter.astro',
              PageSidebar: './src/components/starlight/PageSidebar.astro',
            },
            // Split from a single 4,965-line starlight.css into ordered per-pass
            // files (see docs/ARCHITECTURE.md). Order matters: several later
            // files intentionally override earlier ones at equal specificity, so
            // this array must stay in the same relative order as the original
            // file — do not alphabetize or reorder without checking the affected
            // rules' cascade dependency first.
            customCss: [
              './src/styles/starlight/01-tokens-and-header.css',
              './src/styles/starlight/02-search-and-sidebar-base.css',
              './src/styles/starlight/03-shell-layout-base.css',
              './src/styles/starlight/04-markdown-content-base.css',
              './src/styles/starlight/05-docs-surfaces.css',
              './src/styles/starlight/06-docs-interaction-a.css',
              './src/styles/starlight/07-docs-interaction-b.css',
              './src/styles/starlight/08-search-modal.css',
              './src/styles/starlight/09-search-table-fit.css',
              './src/styles/starlight/10-docs-feedback.css',
              './src/styles/starlight/11-docs-chrome-consistency.css',
              './src/styles/starlight/12-docs-toc-responsive.css',
              './src/styles/starlight/13-docs-rail-alignment.css',
              './src/styles/starlight/14-docs-nav-table.css',
              './src/styles/starlight/15-responsive-containment.css',
              './src/styles/starlight/16-shell-interaction-closeout.css',
              './src/styles/starlight/17-mobile-shell-closeout-a.css',
              './src/styles/starlight/18-mobile-shell-closeout-b.css',
              './src/styles/starlight/19-markdown-tables.css',
              './src/styles/starlight/20-docs-shell-closeout.css',
              './src/styles/starlight/21-mobile-docs-shell-verified.css',
              './src/styles/starlight/22-mobile-docs-correction.css',
              './src/styles/starlight/23-regression-guardrails.css',
              './src/styles/starlight/24-mobile-regression-guardrails.css',
              './src/styles/starlight/25-mobile-overrides-final.css',
              './src/styles/starlight/26-mobile-containment.css',
              './src/styles/starlight/27-release-qa-overrides.css',
              './src/styles/starlight/28-final-interaction-guardrails.css',
            ],
            social: [
              {
                icon: 'github',
                label: 'GitHub',
                href: `https://github.com/${site.social.repo}`,
              },
            ],
            sidebar: [
              {
                label: 'Start',
                items: [
                  { label: 'Overview', link: '/docs/' },
                  { label: 'Installation', link: '/docs/install/' },
                  { label: 'Interface coverage', link: '/docs/interface-coverage/' },
                ],
              },
              {
                label: 'Interfaces',
                items: [
                  { label: 'Desktop app', link: '/docs/desktop/' },
                  { label: 'Terminal UI', link: '/docs/tui/' },
                  { label: 'CLI', link: '/docs/cli/' },
                  { label: 'MCP server', link: '/docs/mcp/' },
                ],
              },
              {
                label: 'Operations',
                items: [
                  { label: 'Operation guide', link: '/docs/operations/' },
                  { label: 'Packet capture', link: '/docs/packet-capture/' },
                ],
              },
              {
                label: 'Reference',
                items: [
                  { label: 'Core library and crates', link: '/docs/core-library/' },
                  { label: 'Result model', link: '/docs/result-model/' },
                ],
              },
            ],
          }),
        ]
      : []),
  ],
  build: {
    // Force-inline the Astro-generated CSS. Our full stylesheet is ~3 KiB
    // gzipped — smaller than the round-trip cost of a render-blocking
    // <link rel="stylesheet">. Lighthouse flagged this as a LCP cost;
    // 'always' replaces the <link> with a <style> in <head>.
    inlineStylesheets: 'always',
  },
});
