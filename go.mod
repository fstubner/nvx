module github.com/fstubner/nvx

go 1.23

// The release workflow builds with 1.26.6, which clears five stdlib
// vulnerabilities govulncheck reports as reachable from DownloadFile,
// ScanVulnerabilitiesBatch and ResolveNpmPackageDetails. Nothing made a local
// build use it, and the install script's NVX_USE_LOCAL_BINARY path ships
// whatever the developer has. A toolchain line makes the floor the same in
// both places without raising the language version the module needs.
toolchain go1.26.6



