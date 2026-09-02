# Ten minutes on a Mac

nvx has never been run by a person on macOS. Its macOS containment is checked
on a hosted GitHub runner every push (`scripts/sandbox-enforcement-macos.sh`),
which says the OS rules hold; it does not say whether installing it and using
it as your Node version manager feels right on a real machine. That is what
this asks for.

Paste back everything the terminal prints. Nothing here needs `sudo`, and the
last step removes it all.

## 1. Install (2 min)

```bash
curl -fsSL https://raw.githubusercontent.com/fstubner/nvx/main/install.sh | sh
```

Then **open a new terminal window** and run:

```bash
nvx doctor
sw_vers && uname -m && echo $SHELL
which -a node npm
```

Please include what `which -a` prints: it shows whether a Homebrew or nvm node
was already there, which changes what the next steps mean.

## 2. Use it as a version manager (3 min)

```bash
nvx install 22
nvx use 22
node -v
mkdir -p ~/nvx-try && cd ~/nvx-try && npm init -y >/dev/null
npm install is-odd
```

Expected: `node -v` prints a v22, and `npm install` prints **one** line from
nvx (`Running in native sandbox: npm install is-odd`) followed by npm's own
output. Anything else nvx prints there is worth pasting.

```bash
echo 20 > .nvmrc
cd .. && cd nvx-try
```

Expected: nvx notices the `.nvmrc` and offers to install Node 20. Say yes, then
`node -v` should print a v20. Say how long the install took if it felt slow.

## 3. Two things that must be refused (3 min)

Still in `~/nvx-try`. An install script that tries to write outside the project.
The path is spelled out by your shell before npm ever runs, on purpose: inside
the sandbox `HOME` points at a throwaway directory, so a script asking for
"the home directory" would be handed the decoy and prove nothing.

```bash
cat > package.json <<EOF
{ "name": "nvx-try", "version": "1.0.0",
  "scripts": { "postinstall": "node -e \\"require('fs').writeFileSync('$HOME/nvx-escape.txt','x')\\"" } }
EOF
grep nvx-escape package.json
npm install
ls -la ~/nvx-escape.txt
```

The `grep` line should show your real home path (`/Users/<you>/nvx-escape.txt`)
baked into the script; if it shows `$HOME` literally, the test is not testing
anything, please say so.

Expected: `ls` says **No such file**. The install itself may report that the
postinstall script failed; that is the refusal working.

A package fetch with an empty egress allowlist:

```bash
echo '{ "isolation": { "network": { "mode": "proxy", "default_allow": [], "prompt_unknown": false } } }' > .nvx-policy.json
npm install is-even
```

Expected: the install **fails** without reaching the registry. Paste the error.

One thing macOS does *not* refuse, on purpose, and we want to see it stated by
a real machine rather than a runner:

```bash
rm .nvx-policy.json
npm exec -c "ls -la $HOME/.ssh"
```

`npm exec` runs its command inside the sandbox, and `$HOME` is again expanded
by your shell first so the listing targets your real `.ssh`, not the decoy.
Expected on macOS: the listing **succeeds**. On Windows and Linux the same
command is denied; on macOS reads are allowed and documented as such. If it is
denied on your Mac, that is news. (If `~/.ssh` does not exist, use any private
directory in your home instead.)

## 4. Remove it (1 min)

```bash
cd ~ && rm -rf ~/nvx-try ~/.nvx
```

and delete the `eval "$(nvx env)"` line the installer added to your shell
profile (`~/.zshrc`, or `~/.bash_profile`).

## If you have Go installed (optional, 3 min)

```bash
git clone https://github.com/fstubner/nvx && cd nvx && go build -o nvx . && \
  chmod +x scripts/sandbox-enforcement-macos.sh && ./scripts/sandbox-enforcement-macos.sh
```

That is the same check CI runs, on your hardware instead of a runner. Paste all
of it.

## What to send

The terminal output from each section, the macOS version and chip, and one
sentence on whether you would keep it as your Node manager. A step that did not
match its "Expected" line is the most useful thing you can report.
