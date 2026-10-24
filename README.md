# interzone

A friendly terminal wizard for
[matrix-docker-ansible-deploy](https://github.com/spantaleev/matrix-docker-ansible-deploy)
(MDAD). Pick a homeserver, tick the bridges, bots and services you want, and
interzone writes the inventory and runs the playbook for you, with a live
progress view.

It is built to keep working during a **country-wide internet blackout**:
every download can come from mirrors inside the country.

## Features

- **Any homeserver:** Synapse, Continuwuity, Tuwunel, Conduit or Dendrite.
  Synapse-only components are greyed out for the others.
- **Everything MDAD offers:** 82 components across clients, auth, storage,
  bridges, bots, monitoring, backups and calls, with search (`/`).
  Required settings are asked for; secrets are generated.
- **Blackout-ready.** Each source can be redirected:

  | What | Default | How it's used |
  |---|---|---|
  | Playbook git | `https://itsjustadream.g0th.ir/spantaleev/matrix-docker-ansible-deploy` | `git clone` / `git pull` |
  | Ansible roles git | `https://itsjustadream.g0th.ir/` | `github.com` URLs in `requirements.yml` are rewritten with git `insteadOf`, so the playbook stays unmodified |
  | Container images | `mechatok.g0th.ir/` | `matrix_container_global_registry_prefix_override` (every image, from every registry) |
  | Docker Engine packages | – | `docker_repo_url` (mirror of `download.docker.com/linux`) |
  | OS packages (APT) | – | Debian/Ubuntu sources on the server are rewritten (with backups) before install |
  | PyPI | – | Installs Ansible into a private venv if it's missing |

  `ctrl+t` on the Network screen tests every source.
- **Checks before it touches anything.** The Review screen logs into the
  server and checks root access, OS, CPU, Python, memory, disk and ports
  80/443/8448, then resolves every DNS record the setup needs and compares
  it with the server's addresses.
- **Checks after deploying.** The playbook's self-check runs at the end and
  lists which services answer and which don't.
- **Manage an existing server:** update, run the self-check, restart, add
  users (Synapse/Dendrite), close registration (Conduit, Continuwuity,
  Tuwunel), back up the Postgres database to this computer, and read any
  service's logs.
- **Fully offline installs** with `interzone bundle` (see below).
- **Certificates that work offline:** Let's Encrypt, a generated self-signed
  wildcard certificate, your own certificate, or no HTTPS.
- **Install locally or over SSH.**
- **Remembers your answers** in `~/.config/interzone/config.json` (mode
  `0600`). Re-run interzone to change anything.

## Build

```sh
make            # static binary ./interzone
```

To build without internet, run `go mod vendor` while you still have access
and build with `go build -mod=vendor`. The resulting binary is static and has
no runtime dependencies besides `git`, `ssh` and Ansible (which interzone can
install itself from a PyPI mirror).

## Use

```sh
./interzone           # start the wizard (or the Manage menu, once installed)
./interzone --reset   # forget saved answers
```

| Key | Action |
|---|---|
| `enter` / `↑↓` / `tab` | move through fields |
| `←→` / `space` | choose / toggle |
| `ctrl+n` / `ctrl+p` | next / previous step |
| `ctrl+r` | reveal secrets |
| `ctrl+s` | save answers |
| `esc` | back |

On the Review screen, `enter` deploys and `s` only writes the files and prints
the `ansible-playbook` command to run later.

### Without the wizard

```sh
interzone apply                # deploy with saved answers; runs the checks first
interzone apply --save-only    # only write the inventory
interzone apply --config answers.json --force --quiet
interzone check                # test mirrors, the server and DNS
```

`apply` exits non-zero if a check fails (unless `--force`), if the run
fails, or if the self-check finds a problem. Answers files are the
`config.json` the wizard writes; missing secrets are generated and saved
back into the file.

### Offline bundle

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
are not included: point interzone at a registry mirror inside your network.

Files:

- playbook: `~/.local/share/interzone/matrix-docker-ansible-deploy`
- generated inventory: `<playbook>/inventory/`
- last deployment log: `~/.config/interzone/last-deploy.log`
- self-signed certificates: `~/.config/interzone/ssl/<domain>/`
- database backups: `~/.local/share/interzone/backups/`
