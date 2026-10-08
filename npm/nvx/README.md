# @fstubner/nvx

nvx runs `npm install` and `npx` inside an OS sandbox on Windows, macOS and
Linux, so a package cannot read your credentials or reach a host you did not
allow. You and your agent type the same commands. It also manages Node.js and
Bun versions.

```sh
npm install -g @fstubner/nvx
nvx help
```

Type the scoped name, `@fstubner/nvx`. The unscoped `nvx` on npm is a different
project. nvx is also not related to Microsoft's NVX micro-VM project
(github.com/microsoft/nvx).

This route writes no shims and edits no profile. Next step: run `nvx doctor`,
which reports what is missing, and `nvx doctor --fix` to repair it.

This package has no install scripts. The binary comes from the one
`@fstubner/nvx-<platform>` package in `optionalDependencies` that matches your
`os` and `cpu`, and `bin/nvx.js` hands over to it.

Documentation, other ways to install, and the source are at
https://github.com/fstubner/nvx
