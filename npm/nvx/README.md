# @fstubner/nvx

Contain what your agent installs. nvx runs installs and ad-hoc tool runners
(npx, bunx) from npm, yarn, pnpm and bun inside an OS sandbox. It also manages
Node.js and Bun versions.

```sh
npm install -g @fstubner/nvx
nvx help
```

The package is `@fstubner/nvx`. The unscoped `nvx` on npm is a different project.

This route writes no shims and edits no profile. Next step: run `nvx doctor`,
which reports what is missing, and `nvx doctor --fix` to repair it.

This package has no install scripts. The binary comes from the one
`@fstubner/nvx-<platform>` package in `optionalDependencies` that matches your
`os` and `cpu`, and `bin/nvx.js` hands over to it.

Documentation, other ways to install, and the source are at
https://github.com/fstubner/nvx
