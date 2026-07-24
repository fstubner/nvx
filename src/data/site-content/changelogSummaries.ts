// Optional hand-written one-paragraph summary per release tag (e.g.
// 'v0.3.0'), shown above the raw release-note body on /changelog/. Leave a
// version out and the changelog page auto-derives a summary from the
// release body instead (see scripts/changelog/summarize.ts) — this file is
// only for when you want to say something more editorial than the raw
// notes support.
export const changelogSummaries: Record<string, string> = {
  'v0.3.0':
    'This release ships the redesigned desktop workspace, richer scan status data, probe concurrency controls in interactive interfaces, refreshed public docs, and packaging fixes across release channels.',
  'v0.2.6':
    'This release fixes desktop version reporting and Windows title-bar controls, so installed GUI builds identify themselves correctly and basic window actions work as expected. It also completes CLI/TUI refactors that make future interface changes easier to review and test.',
  'v0.2.5':
    'This release closes a DNS resolver security advisory, fixes Windows subnet detection so discovery and sweep find real LAN hosts, resets desktop history row styling in WebView2, and extends package publishing for GUI installers.',
  'v0.2.4':
    'This release repairs GUI installer publishing after the previous release built packages but failed to attach them. It also updates the AUR deploy action so Linux package publishing can complete again.',
  'v0.2.3':
    'This release gets GUI installer builds moving again by aligning the Tauri JavaScript and Rust versions, and fixes the AUR packaging handoff. CLI packages were usable from v0.2.2, but GUI artifacts needed these release-pipeline fixes.',
  'v0.2.2':
    'This is a release-pipeline recovery build. It refreshes Cargo.lock so locked release builds can run reproducibly after dependency bumps, giving package-manager users a working replacement for the failed v0.2.1 artifacts.',
  'v0.2.1':
    'This release makes NetsCLI easier to install outside a Rust toolchain by publishing desktop installers and signed release assets. It also adds concurrency tuning for networks that struggle with large parallel scans.',
  'v0.2.0':
    'This release turns the initial scanner into a more complete distribution: mDNS discovery, shell completions, man pages, package-manager templates, signed artifacts, typed core errors, and leaner optional features make NetsCLI easier to install, automate, and integrate.',
  'v0.1.1':
    'This release cleans up the first public version with crate documentation, security notes, a structured changelog, and release workflow fixes so binaries and pcap variants can be produced reliably.',
  'v0.1.0':
    'This is the first public NetsCLI release: one Rust core powers the CLI, terminal UI, desktop app, and MCP server, with structured output and cross-platform binaries for early users.',
};
