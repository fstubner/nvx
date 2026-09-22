// Shared type definitions for the site content modules under
// site/src/data/site-content/. Assembled into the public `SiteData` shape
// by site/src/data/site.ts — that's the only module other files should
// import `site` from.

export interface Meta {
  /** Canonical site URL without trailing slash. */
  domain: string;
  /** <title> */
  title: string;
  /** <meta name="description"> */
  description: string;
  /** Short form used in OG / Twitter cards. Falls back to description. */
  ogDescription?: string;
  /** Site name for OG. */
  siteName: string;
  author: { name: string; url: string };
  /** Absolute URL to the OG/Twitter share image. */
  ogImage: string;
  /** What the share image shows, for the `twitter:image:alt` tag. Every
   *  page's card uses the same asset, so one description covers them all. */
  ogImageAlt: string;
  /** Favicon + apple-touch-icon. */
  faviconPath: string;
  themeColor: string;
}

/** schema.org SoftwareApplication facts that differ per product.
 *
 * Required on purpose: these were literals in the JSON-LD where nothing made
 * a new site revisit them. The rationale is in meta.ts, next to the values. */
/** One product surface the changelog summariser can name. */
export interface ChangelogSurface {
  /** Matched against the release body. */
  pattern: RegExp;
  /** How the summary names it. */
  label: string;
}

/** One column of the competitor comparison. */
export interface ComparisonColumn {
  name: string;
  /** The product this site is for. Gets the tint and the left rule. */
  highlight?: boolean;
}

/** One capability row. `cells` is one entry per column, in column order.
 *  A cell is '✓', '—', or short text where the answer needs a word. */
export interface ComparisonRow {
  feature: string;
  cells: string[];
}

export interface AppSchema {
  applicationCategory: string;
  /** Free text narrowing the category for this product. */
  applicationSubCategory: string;
  /** Platforms, as a human list: 'Windows, macOS, Linux'. */
  operatingSystem: string;
  license: string;
  /** The language the PRODUCT is written in, not the site. */
  programmingLanguage: string;
  /** Price as a string. '0' for free. */
  price: string;
  priceCurrency: string;
}

export interface Branding {
  /** Path to the wordmark image served from /. Used on the dark theme, and on
   *  both when `wordmarkLight` is absent. */
  wordmark: string;
  /** Optional light-theme wordmark. A single asset cannot carry lettering that
   *  reads on both grounds -- it has to be light on one and dark on the other --
   *  so a product whose mark contains type ships two files and the bars swap
   *  them. Omit it and `--ui-mark-filter` handles the light theme instead,
   *  which suits a mark with no lettering to invert. */
  wordmarkLight?: string;
  /** Shown in nav at 160px desktop / 132px mobile. */
  wordmarkAlt: string;
  /** Links + underline accent colour. */
  accentGradient: string;
  /** Background fill. */
  bg: string;
  /** Default body text colour. */
  fg: string;
}

import type { Hero, HeroDownload } from './hero-types';

export type { Hero, HeroCommands, HeroDownload } from './hero-types';

export interface SurfaceCard {
  title: string;
  /** HTML allowed — typically short paragraph, may contain <code>. */
  body: string;
  /** If set, renders an image panel. */
  image?: {
    src: string;
    webp?: string;
    alt: string;
    width: number;
    height: number;
  };
  /** If set instead of image, renders a stylised code block. HTML allowed. */
  codeHtml?: string;
  /** If true, flips text and visual sides for alternating rhythm. */
  flip?: boolean;
  /** Optional per-platform download buttons rendered below the body.
   *  Used by the Desktop card to surface .msi / .dmg / .deb / .AppImage
   *  installers from the latest GitHub release. */
  downloads?: SurfaceDownload[];
}

export interface SurfaceDownload {
  /** Visible button label, e.g. "Windows (.msi)". */
  label: string;
  /** Direct download URL. Use the /releases/latest/download/ form so
   *  the buttons auto-track the latest release without site updates. */
  url: string;
  /** Optional secondary line below the label, e.g. "Apple Silicon". */
  hint?: string;
}

export type Platform = 'windows' | 'macos' | 'linux';

export interface InstallEntry {
  label: string;
  /** Shell command(s) shown monospace with copy button. Omit when this
   *  entry is a direct download — set `href` instead. */
  command?: string;
  /** Direct download URL. Entries with an `href` render as a link rather
   *  than a copyable command, for the installer artifacts that have no
   *  package-manager equivalent (.msi / .dmg / .deb / .AppImage). */
  href?: string;
  /** Optional small hint, rendered under the label. Used to warn about
   *  the unsigned installers before someone hits a Gatekeeper or
   *  SmartScreen dialog with no explanation. */
  hint?: string;
}

/** Install routes for one OS, split by which thing you are installing.
 *
 *  Both lists follow the same convention as before: position 0 is the
 *  recommended entry and renders as the hero card; the rest render as
 *  alternative rows in array order.
 */
export interface PlatformInstall {
  /** Command-line routes: the binary itself, however it is installed. */
  cli: InstallEntry[];
  /** Desktop application routes. Leave empty for a product with no
   *  desktop build; the section renders without that column. */
  desktop: InstallEntry[];
}

export interface TryCommand {
  /** Short comment rendered above the command. */
  comment: string;
  /** Shell command copied by the row-level copy button. */
  command: string;
}

export interface FaqItem {
  group: string;
  q: string;
  /** Plain text used verbatim in both the visible section and JSON-LD. */
  a: string;
  /** Rich HTML variant for the visible section. Falls back to `a`. */
  aHtml?: string;
}

export interface BuiltWithEntry {
  name: string;
  url: string;
}

export interface SocialProof {
  /** GitHub repo in "owner/name" format. Used to fetch stars + download counts. */
  repo: string;
}

export interface Analytics {
  /** Cloudflare Web Analytics beacon token. Omit to disable. */
  cloudflareToken?: string;
}

export interface SectionCopy {
  heading: string;
  /** HTML allowed — typically short tagline with an anchor link. */
  leadHtml: string;
}

export interface Modules {
  /** Whether the /docs/ Starlight section exists for this product. When
   *  false, nav/footer/404 stop linking to /docs/ and in-copy "Full docs →"
   *  references are omitted — but the Starlight integration itself must
   *  also be removed from astro.config.mjs (see the comment there). */
  docs: boolean;
  /** Whether the /changelog/ page exists for this product. When false,
   *  nav/footer stop linking to /changelog/. */
  changelog: boolean;
}

/** How the landing page arranges its hero and the rhythm below it. */
export type LandingLayout = 'centered' | 'split';

/** A section the landing page can render. Adding one here means adding a
 *  component for it in src/pages/index.astro's map. */
export type LandingSection = 'hero' | 'surfaces' | 'reach' | 'compare' | 'install' | 'faq';

/** One target in the "what an install can reach" comparison. */
export interface ReachRow {
  /** The path or destination, rendered as code. */
  target: string;
  /** What it is, in plain words. */
  what: string;
  /** Its state under any other version manager, which is npm's own answer. */
  plain: string;
  /** Its state from inside a contained install. */
  contained: string;
}

/** The platform caveat that sits beside the comparison, not under it. */
export interface ReachNote {
  heading: string;
  /** HTML allowed. */
  bodyHtml: string;
  /** Where the evidence for the rows comes from. HTML allowed. */
  measuredHtml: string;
}

/** One group in the docs sidebar, as Starlight expects it. */
export interface DocsSection {
  label: string;
  items: { label: string; link: string }[];
}

export interface SiteData {
  meta: Meta;
  branding: Branding;
  modules: Modules;
  hero: Hero;
  /** Desktop installers offered in the hero. Empty for a product that
   *  ships no desktop build. */
  heroDownloads: HeroDownload[];
  /** Visible headings + leads for each main section. */
  copy: {
    surfaces: SectionCopy;
    install: SectionCopy;
    faq: SectionCopy;
  };
  surfaces: SurfaceCard[];
  install: {
    /** Per-OS install routes, each split into CLI and desktop groups. */
    byPlatform: Record<Platform, PlatformInstall>;
    tryCommands: TryCommand[];
    binariesNote: string;
    /** Build-from-source command, listed in /llms.txt after the quickstart. */
    fromSource: string;
    /** Caveats /llms.txt prints under the install list, one line each. */
    notes: string[];
  };
  faq: FaqItem[];
  builtWith: BuiltWithEntry[];
  social: SocialProof;
  analytics: Analytics;
  /** Version string published on crates.io / used in structured data. */
  version: string;
}
