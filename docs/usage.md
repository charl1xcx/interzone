# Using interzone

The short version is in the [README](../README.md).


```sh
./interzone           # start the wizard (or Manage, once installed; esc there leads to Welcome)
./interzone --reset   # start fresh (or R on the Servers screen): every server's answers,
                      # playbook copies and history are moved aside to *.old-DATE, not deleted
./interzone -no-mouse # leave the mouse to the terminal
./interzone -layout compact # phone layout, whatever the size (or wide, or auto)
./interzone -theme nord     # colours for one run (ctrl+y switches and remembers them)
INTERZONE_MOTION=off ./interzone # keep the logo and progress bars still
./interzone -plain          # no colours, ASCII boxes and bars, no animation: for screen
                            # readers and logs (or INTERZONE_PLAIN=1)
```

The wizard has four steps before the deploy: **Server** (where it runs,
where downloads come from, HTTPS certificates, networking), **Homeserver**
(a preset, or only the homeserver),
**Components** (pick them), **Settings** (what they need, the admin
account and each component's address, with Advanced beside it: hand-written
vars.yml lines in a box per playbook role they belong to, or as plain text
with `ctrl+o`; tab across) and **Review**.

The menu on the left shows where you are: the setup steps, the Manage
screens (with the action running, if any), the mirror checks and your
servers. Each section folds on its own: click its heading to open or
close just that one, and going to a screen opens its section without
closing the others; interzone remembers which are open. `◂ fold` at its
bottom folds the whole menu to a rail of icons, and terminals narrower
than 100 columns always get the rail.

The menu works from the keyboard too: `ctrl+b` moves the keys to it
(unfolding it if needed), `↑↓` move, `←→` fold and unfold a section,
`enter` opens what's selected and `esc` goes back to the screen.

On a phone (Termux, or any narrow or portrait terminal) interzone switches
to a compact layout: a one-line header saying where you are, no sidebar or
borders, and lists without their side columns. Instead of key hints there's
a bar of buttons at the bottom: `‹ back`, `≡ menu` (or `ctrl+b`, a grid of
tiles to tap that scrolls with a swipe; the arrow keys move across the
tiles and `enter` opens one), the
screen's main action (`next ›`, `run ›`…) and `⋯`, which lists every
other key of the screen and the global ones (save, layout, theme, quit) as
rows to tap. When the screen gets short, as when the on-screen keyboard is
up, descriptions shrink to one line and forms show only the field you're
typing in, with a one-line summary of each of the others. `ctrl+l`, or `◫`
at the bottom of the menu, cycles auto, compact and wide and remembers the
choice; `-layout` picks one for a single run.

`ctrl+y`, or `◐` under it, switches the colours between interzone,
Catppuccin, Nord, Gruvbox, Dracula, Solarized, Tokyo Night, Rosé Pine,
Everforest, Kanagawa, One Dark, Synthwave and mono; each has a light
and a dark variant that follows the terminal's background.

| Key | Action |
|---|---|
| `enter` / `↑↓` / `tab` | move through fields |
| `←→` / `space` | choose / toggle |
| `ctrl+n` / `ctrl+p` | next / previous step |
| `ctrl+r` | reveal secrets |
| `ctrl+s` | save answers |
| `ctrl+b` | go to the menu: `↑↓` `←→` to move and fold, `enter` to open, `esc` to go back (compact layout: open the menu, like `≡ menu`) |
| `ctrl+l` | next layout: auto, compact, wide |
| `ctrl+y` | next theme |
| `ctrl+k` | go to: type part of a screen's or Manage action's name, `enter` goes there |
| `?` | every key of the screen you're on, and the ones that work anywhere (not while typing) |
| `esc` | back |

Everything also works with the mouse: click a menu entry, a field, an option,
a checkbox or a key in the bar at the bottom. A click or tap anywhere on a
list row opens it; Manage actions that change the server right away (update,
restart, check, back up …) ask for a second click first. The wheel scrolls.
While interzone has the mouse, hold `shift` to select text in most terminals, or start it with
`-no-mouse`.

Anything that asks before acting (rolling back, restoring, deactivating a
user) shows what it will do in an amber box, and is confirmed with `y`,
`enter`, the same key again or a click on the same row; `esc` or moving
away cancels. What can't be undone (uninstalling, deleting a room) asks
for a word to be typed instead. After a quick action from Manage (update,
check, restart, image check, scheduled backups) succeeds, interzone returns
to Manage by itself a few seconds later; press any key to stay on the
result.

On the Review screen, `enter` shows what the deploy will do (install or
update, how long it may take, what changes in `vars.yml`, which checks
failed) and `y` then deploys; `s` only writes the files and prints the
`ansible-playbook` command to run later. The page scrolls as a whole with `↑↓`.

## Without the wizard

```sh
interzone apply                # deploy with saved answers; runs the checks first
interzone apply --save-only    # only write the inventory
interzone apply --config answers.json --force --quiet
interzone check                # test mirrors, the server and DNS
interzone dns                  # the DNS records to create, as zone-file lines
interzone mirrors              # test every known in-country mirror and what it carries
interzone apply --latest       # update a playbook held back by a rollback
interzone changes              # what an update would bring
interzone images               # can every container image be pulled?
interzone status               # load, disks, certificates, containers
interzone restore              # list backups; 'restore FILE' restores one
interzone rollback --list      # recorded deploys; 'rollback N' goes back to one
interzone servers              # saved servers; 'servers use NAME', 'servers rm NAME'
interzone import PATH          # take over a playbook set up by hand (-name, -dry-run)
interzone self-update          # install the newest release
INTERZONE_SERVER=work interzone apply   # any command, for another saved server
```

`apply` exits non-zero if a check fails (unless `--force`), if the run
fails, or if the self-check finds a problem. Answers files are the
`config.json` the wizard writes; missing secrets are generated and saved
back into the file.

## Offline bundle

For a machine with no internet at all, make a bundle somewhere that has it:

```sh
interzone bundle -o interzone-bundle.tar.gz
```

It holds the playbook with every Ansible role already installed, the Python
packages for Ansible (built for the bundling machine's OS, CPU and Python
version), and the interzone binary. Copy it over, then:

```sh
tar xzf interzone-bundle.tar.gz
./interzone-bundle/interzone unbundle interzone-bundle
./interzone-bundle/interzone
```

Deploys then use the bundled playbook and roles when the git mirror is
unreachable, and install Ansible from the bundled packages. Container images
are not included: point interzone at a registry mirror inside your network,
or pick IPFS on the Server step (a bundle made with IPFS on carries the IPFS
daemon for both x86 and ARM servers).

Files:

- playbook: `~/.local/share/interzone/matrix-docker-ansible-deploy`
- generated inventory: `<playbook>/inventory/`
- last deployment log: `~/.config/interzone/last-deploy.log`
- self-signed certificates: `~/.config/interzone/ssl/<domain>/`
- database backups: `~/.local/share/interzone/backups/`
- deploy history for rollback: `~/.config/interzone/history/<server>/`
- other servers: answers in `~/.config/interzone/servers/`, playbooks in
  `~/.local/share/interzone/servers/<name>/`

## Building and testing


```sh
make            # static binary ./interzone
```

To build without internet, run `go mod vendor` while you still have access
and build with `go build -mod=vendor`. In a blackout, interzone's mirror has
Go and every module it builds with (also in the IPFS swarm's packages part):

```sh
curl -O https://infamous.g0th.ir/go/dl/go1.27.1.linux-amd64.tar.gz   # or linux-arm64, darwin-…
sudo tar -C /usr/local -xzf go1.27.1.linux-amd64.tar.gz && export PATH=/usr/local/go/bin:$PATH
GOPROXY=https://infamous.g0th.ir/go GOSUMDB=off GOTOOLCHAIN=local make
```

`go.sum` still checks every module. The resulting binary is static and has
no runtime dependencies besides `git`, `ssh` and Ansible (which interzone can
install itself from a PyPI mirror).

`make test` runs what CI runs: gofmt, `go vet` and `go test -race ./...`.
`go test ./...` includes layout tests that draw the main screens at phone
(46×40) and desktop (120×36) size and compare them with
`internal/ui/testdata/layout`; after an intended change, accept the new
drawings with `go test ./internal/ui -run Layout -update`. The README's
screenshots are retaken with `scripts/screenshots/shots.sh all` (tmux, Python
with Pillow and fontTools, and the DejaVu and Noto symbol, math and colour
emoji fonts), about 15 seconds, against a demo server that doesn't exist;
`python3 scripts/screenshots/social.py docs/social-preview.png` redraws the
link preview from them.
