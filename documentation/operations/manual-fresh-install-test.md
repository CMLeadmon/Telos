# Manual fresh-install test on this Ubuntu machine

A step-by-step runbook for installing Telos from this checkout on a clean machine and testing every module by hand. It ends with a
results sheet and a teardown that puts the machine back as it was.

> **Honesty note.** The agent that wrote this could not run it on this host (no `sudo` password, and Podman is not installed).
> Every step is taken from [install.md](install.md), [guided-integrations.md](guided-integrations.md) and the same procedure that ran
> on the Ubuntu 26.04.1 reference VMs (rehearsal evidence, see `docs/implementation/standalone-beta-progress.md`). The host-specific
> parts, such as the `apt` installs, `/etc/hosts` and the browser steps, are untested here. If a step behaves differently from what is
> written, **stop and note it in the results sheet**; that difference is a finding.

## 0. Read this first

| | |
|---|---|
| **What you will end with** | A working single-machine Telos node at `https://telos.test` with all four modules, served over HTTPS from a private test CA, with generated test media. |
| **Time** | About 1.5 to 2 hours: 10–20 min of package installs, 10–20 min building the images, 20 min of guided setup, the rest testing. |
| **Needs** | `sudo` rights (you are in the `sudo` group), internet access (apt, PyPI, docker.io, Go and npm module downloads), about 15 GiB of disk and the memory in section 1. |
| **Changes on this machine** | Installs Podman and a few packages; creates `/opt/telos`, `/opt/telos-tools`, `/var/lib/telos`, `/var/lib/telos-tls`, `/mnt/storage/shared`, `/usr/local/bin/telos`; adds lines to `/etc/hosts` and `/etc/environment`; trusts one test certificate authority; publishes ports 80 and 443 on **all network interfaces** while the stack runs. |
| **Undo** | Section 11 reverses every one of those. |
| **Not covered** | Public DNS and Let's Encrypt (this machine has no public name), the encrypted off-node backup (needs a separate restic repository), upgrades and the Linux desktop app. |

Use `main` at or after the commit that added this file. An older checkout of `main` (the 29 August state, `237692f`) does not contain
the standalone installer, the pinned images, the node CLI changes or the fixes described here.

Conventions: commands are for a normal terminal as user `carter`; `sudo` appears where it is required. `$SRC` is the checkout.

```bash
export SRC="$HOME/Projects/Telos"
cd "$SRC" && git branch --show-current && git log --oneline -1   # expect: main and the newest commit
```

## 1. Machine check and making room

What this machine looked like when the runbook was written:

| Requirement | This machine | Status |
|---|---|---|
| Ubuntu 24.04+ x86-64 (26.04 is the verified release) | Ubuntu 26.04.1 LTS, x86_64, kernel 7.0 | ok |
| 4+ CPU cores, 16+ GiB memory, 20+ GiB free disk | 16 cores, 26 GiB, 700+ GiB free | ok **only if the test VMs are stopped** (below) |
| Ports 80 and 443 free | free | ok |
| Podman 5+ (rootful) and `podman-compose` 1.6.0 | **not installed** | install in section 2 |

**Stop the two reference VMs first.** They hold about 16.5 GiB of this machine's RAM, leaving about 6 GiB, and the Telos stack's
limits add up to roughly 14 GiB. (They are only my test machines; stopping them loses nothing.)

```bash
~/.local/telos-vm/vm.sh stop verifier
~/.local/telos-vm/vm.sh stop node
sleep 15; free -g | awk '/Mem:/{print "available GiB:", $7}'      # expect 20 or more
ss -ltn | awk '$4 ~ /:(80|443)$/' | wc -l                          # expect 0 (nothing on 80/443)
```

Confirm the machine is genuinely fresh for Telos (all of these should print nothing or "missing"):

```bash
ls -d /opt/telos /var/lib/telos /var/lib/telos-tls /mnt/storage/shared 2>&1 | grep -v "No such file" || echo "none exist: fresh"
command -v podman telos 2>&1 || echo "podman and telos: missing (expected)"
```

If any directory exists, it is a previous attempt: do section 11 first, or choose another machine.

## 2. Install Podman and the compose provider

```bash
sudo apt-get update
sudo apt-get install -y podman skopeo buildah uidmap python3-venv python3-yaml python3-dotenv curl openssl
podman --version                                   # expect: podman version 5.x (26.04 ships 5.7.0)
sudo podman run --rm docker.io/library/alpine:3.22.6 echo ok      # expect: ok   (rootful container smoke test)
```

Telos uses `podman-compose` **1.6.0**, pinned by hash, installed into its own environment. (Ubuntu's own package is too old and the
tool refuses it.)

```bash
sudo python3 -m venv --system-site-packages /opt/telos-tools/podman-compose
cat > /tmp/podman-compose.req <<'EOF'
podman-compose==1.6.0 --hash=sha256:98e48af25d44c10ecef948163bfcca002e32aa8bbb7e66593fbd4faff7270c30
EOF
sudo /opt/telos-tools/podman-compose/bin/python -m pip install --require-hashes --no-deps -r /tmp/podman-compose.req
/opt/telos-tools/podman-compose/bin/podman-compose --version       # expect a line "podman-compose version 1.6.0"
```

Make `sudo` see the provider:

```bash
echo 'PODMAN_COMPOSE_PROVIDER=/opt/telos-tools/podman-compose/bin/podman-compose' | sudo tee -a /etc/environment
sudo env | grep PODMAN_COMPOSE_PROVIDER        # expect the line you just added
```

If that grep prints nothing, run the later commands as `sudo env PODMAN_COMPOSE_PROVIDER=/opt/telos-tools/podman-compose/bin/podman-compose telos …`.

## 3. Preflight and install the command

```bash
cd "$SRC"
./install.sh --check          # read-only
```

Expected: every line ✔ (host Ubuntu 26.04 reference release, x86_64, kernel, Podman, podman-compose, ports 80 and 443 free,
resources, tools) and no "unverified combination" warning. **If anything is ✘ stop here.**

```bash
sudo ./install.sh --yes       # copies the tree to /opt/telos (without .git, node_modules, build output) and links /usr/local/bin/telos
telos --help | head -12       # lists init, configure, doctor, start, status, stop, migrate
```

`install.sh` installs the command only: no secrets, no data. Nothing is running yet.

## 4. A name and a certificate for this machine

This machine has no public DNS name, so the test uses `telos.test`, a name only this machine knows, and a throw-away private
certificate authority. (Production uses public certificates; Telos never disables certificate checks.)

```bash
echo '127.0.0.1 telos.test' | sudo tee -a /etc/hosts
getent hosts telos.test                                            # expect: 127.0.0.1 telos.test

sudo bash "$SRC/tests/rehearsal/make-private-ca.sh" /var/lib/telos-tls telos.test
sudo ls -l /var/lib/telos-tls                                      # expect ca.crt, ca.key, server.crt, server.key
```

The script creates the CA and a 30-day server certificate, makes the directory and `server.key` readable by Traefik's user (UID
65532) and keeps `ca.key` root-only. After 30 days the certificate expires: re-run the script into a new directory to continue.

Make this machine's command-line tools trust the test CA (so `curl` works without `-k`):

```bash
sudo install -m 0644 /var/lib/telos-tls/ca.crt /usr/local/share/ca-certificates/telos-test-ca.crt
sudo update-ca-certificates                                        # expect: 1 added
```

For the **browser** (done in section 7): Firefox keeps its own list. Settings → Privacy & Security → View Certificates → Authorities →
Import → `/var/lib/telos-tls/ca.crt` (copy it somewhere you can read first: `sudo cp /var/lib/telos-tls/ca.crt ~/telos-test-ca.crt && sudo chown $USER ~/telos-test-ca.crt`), tick
"Trust this CA to identify websites". Chrome and Chromium on Ubuntu use the system store only when launched with the NSS setup; the
simplest is to use Firefox, or import the same file under Settings → Privacy and security → Security → Manage certificates → Authorities.

## 5. Initialize the node

```bash
sudo telos init --domain telos.test --acme-email ops@telos.test
```

Creates `/var/lib/telos/node.env` (root-only, generated secrets, never printed), the signing keys and the storage tree under
`/mnt/storage/shared` (owned by you, because `sudo` records your user id as the application identity). **`init` never overwrites
anything and has no `--force`.** If it refuses, read why; do not delete a node you want to keep.

Tell Telos the certificate comes from your private CA (two lines appended to `node.env`):

```bash
printf 'TELOS_PRIVATE_CA=1\nTELOS_TLS_PATH=/var/lib/telos-tls\n' | sudo tee -a /var/lib/telos/node.env >/dev/null
sudo grep -E '^(TELOS_DOMAIN|TELOS_PUBLIC_ORIGIN|TELOS_PRIVATE_CA|TELOS_TLS_PATH|STORAGE_PATH|TELOS_ENV)=' /var/lib/telos/node.env
```

Expected: domain `telos.test`, origin `https://telos.test`, `TELOS_PRIVATE_CA=1`, the TLS path, storage `/mnt/storage/shared`,
`TELOS_ENV=production`. Each key must appear exactly once.

```bash
sudo telos doctor
```

At this point `doctor` is expected to report **only** the media-service settings as not yet configured (Jellyfin token and library
ids, Grimmory library ids); the exact wording may differ. Anything else, such as the certificate being unreadable, a port in use or a placeholder secret, is a
defect to fix now; `doctor` names one repair per defect. It never changes anything.

## 6. Test media, then guided Jellyfin and Grimmory setup

Create small, legally unencumbered test media (a 20-second test-pattern film, a tone, a short EPUB). It needs `ffmpeg`:

```bash
sudo apt-get install -y ffmpeg
bash "$SRC/tests/rehearsal/make-fixtures.sh" /mnt/storage/shared          # as you, not root: you own the storage tree
find /mnt/storage/shared -type f | sort
```

Expect `media/movies/Telos Test Film (2026)/….mp4`, `media/music/Telos Test Artist/Telos Test Album/01 Test Tone.mp3`,
`books/Telos Test Book.epub`.

Now the guided setup. It starts only Jellyfin, Grimmory and their dependencies, publishes their admin pages on **127.0.0.1 only**,
waits while you configure both, then validates and records the settings. It needs a real terminal (it uses hidden prompts). It first
builds the gateway image (a few minutes, so the validation container exists) and pulls the upstream images.

```bash
sudo telos configure integrations --setup
```

When it prints the two local addresses, in a browser **on this machine** (no tunnel needed):

**Jellyfin — http://127.0.0.1:8096**

1. Finish the first-run wizard: choose any admin name and a password you will remember (this is Jellyfin's own account, not a Telos login).
2. Add libraries: type **Movies**, folder **/data/media/movies**; type **Music**, folder **/data/media/music**. Let them scan.
3. Dashboard → API Keys → add one named `telos`; copy the key. You will paste it at the first prompt.

**Grimmory — http://127.0.0.1:6060**

1. Create the administrator the gateway will use: username **telos-gateway**. For the password use the one Telos generated:
   `sudo grep '^GRIMMORY_ADMIN_PASSWORD=' /var/lib/telos/node.env | cut -d= -f2-` (read it, do not paste it anywhere else).
2. Create a library over **/books** (name it anything, for example "Telos Books"; if asked how to scan, choose "file as book", which is right for the single test EPUB).

Back in the terminal press **Enter** (the "both set up" prompt), then:

- **Jellyfin API key:** paste the key (nothing is echoed).
- **Grimmory gateway account:** press Enter to accept `telos-gateway`.
- **Grimmory password:** press Enter (blank keeps the stored one).
- **Expose which libraries?** The command lists each service's libraries as `name (id …)`. Enter the numbers of the two Jellyfin
  libraries, then the Grimmory library.

Expected end: "validated", the setup containers removed, **both loopback ports closed**. Check:

```bash
ss -ltn | awk '$4 ~ /:(8096|6060)$/' | wc -l          # expect 0
sudo telos doctor                                      # expect: no defects
```

If it stops with `jellyfin_restart_pending`, Jellyfin only applies its new Base URL after a restart; run the command again after
`sudo telos start` (section 7) or follow the message.

## 7. Build and start the stack

The first start builds three images from source (the gateway, the web client, the egress proxy) and pulls the rest. It needs the
network and takes 10–20 minutes. Do not interrupt it.

```bash
sudo telos start --build
sudo telos status
```

Expected `status` (the Jellyfin and Grimmory lines may say *starting* for up to a minute, then *healthy*):

```text
Telos stack  (podman)
  ✔  telos-traefik   healthy        ✔  telos-core    healthy      ✔  telos-client    healthy
  ✔  telos-migrate   completed      ✔  telos-postgres healthy     ✔  telos-redis     healthy
  ✔  telos-egress-proxy healthy     ✔  telos-clamav  healthy      ✔  telos-jellyfin  healthy
  ✔  telos-grimmory-db healthy      ✔  telos-grimmory healthy     ✔  telos-prometheus healthy
Gateway dependencies  (ok):  postgres, redis, outbox, storage, jellyfin, grimmory — all ✔
12/12 services up
```

If `status` shows "Host network … forwarding OFF" `telos start` repairs it automatically; if it could not, it prints the command.

Prove HTTPS and the front door from the command line (these use the CA you trusted in section 4):

```bash
curl -sS -o /dev/null -w "https health: %{http_code}\n" https://telos.test/api/v1/health      # expect 200
curl -sS -o /dev/null -w "http redirect: %{http_code} -> %{redirect_url}\n" http://telos.test/ # expect 301 -> https://telos.test/
curl -sS https://telos.test/api/v1/health | python3 -m json.tool | head -20                    # status "ok" and one check per dependency
sudo ss -ltnp | awk '$4 ~ /:(80|443)$/'                                                         # only these two published ports
```

## 8. Test the product by hand (the main test)

Open **https://telos.test** in the browser where you imported the CA. You should see a lock icon and **no certificate warning**.
(A warning means the CA is not trusted in that browser: fix section 4, do not click through.)

### 8.1 Claim ownership (first boot)

The first visit offers "first boot — claim ownership". You need the bootstrap token from the node file (read it once, type or paste
it into the page, and do not share it):

```bash
sudo grep '^TELOS_BOOTSTRAP_TOKEN=' /var/lib/telos/node.env | cut -d= -f2-
```

Choose a username (3–32 characters: `a-z 0-9 . _ -`, starting with a letter or digit) and a password of **15–128 characters**.

| # | Check | Expected | Result |
|---|---|---|---|
| 1 | Submit with a wrong token | Rejected: "bootstrap token rejected" | |
| 2 | Submit with a 10-character password | Rejected: password must be 15–128 characters | |
| 3 | Submit with the right token and a good password | You are signed in as the owner | |
| 4 | Sign out, sign in again | Works; the session survives a page reload | |
| 5 | Try to claim ownership again from the command line: `curl -sS -X POST https://telos.test/api/v1/auth/bootstrap -H 'Content-Type: application/json' -d '{"username":"intruder","password":"a-fifteen-char-pw","token":"<the real token>"}'` | Refused with HTTP 403 `already_bootstrapped` ("An Owner already exists"); the sign-in page no longer offers ownership | |

### 8.2 The four modules and commentary

| # | Module | Do | Expected | Result |
|---|---|---|---|---|
| 6 | Chat | Open a channel, send a message, edit it, reply in a thread, react, pin | Each works and updates without a reload; message survives a reload | |
| 7 | Chat | Open the same channel in a second window as another user (after 8.3) | Messages appear live in both; presence shows both | |
| 8 | Stream | Open Stream; play "Telos Test Film" | Picture and sound play (a test pattern and a tone); seeking works | |
| 9 | Stream | Play "Test Tone" | Audio plays | |
| 10 | Library | Open "Telos Test Book" in the reader | Pages render; a highlight can be added and persists after reload | |
| 11 | Files | Upload a small text file and a small image | Upload completes (it is virus-scanned); both download byte-identical | |
| 12 | Files | Create a folder, move a file in, delete a file | Works; the deleted file is gone after reload | |
| 13 | Commentary | Comment on the film, on the uploaded file, and highlight in the book; reply to a comment; edit and delete your own | Private by default; sharing it makes it visible to others; replies thread | |
| 14 | Settings | Change **Appearance** (theme), edit **Profile**, look at **Security**; the owner also sees **Members, Invites, Roles, Channels** | Persist after reload | |

### 8.3 Invites, roles and a second user

| # | Check | Expected | Result |
|---|---|---|---|
| 15 | Settings → **Invites** → create an invite | An invite link/code is shown once | |
| 16 | Open the invite in a **private window**, choose a username and a 15+ character password | Second user is created and signed in with member rights | |
| 17 | As the member, look at Settings | **Members, Invites, Roles and Channels are not offered** (they are administrator-only); an administrator-only API call such as `GET /api/v1/admin/…` is refused | |
| 18 | Reuse the same invite | Refused (single use) | |
| 19 | As owner, Settings → **Members** / **Roles**: change the member's role or remove them | Takes effect on their next request | |
| 20 | Resize the window to a phone width (or use the browser's device mode) | Layout adapts; navigation still reaches all four modules | |

### 8.4 Restart and data survival

```bash
sudo telos stop && sudo telos start && sudo telos status
```

Expected: 12/12 up again within a few minutes, you are still signed in (or can sign in again), and the messages, files, highlights
and comments from section 8.2 are all still there.

## 9. Security spot checks

| # | Check | Command | Expected | Result |
|---|---|---|---|---|
| 21 | Only 80 and 443 are published by Telos | `sudo ss -ltnp \| awk '$4 ~ /:(80\|443)$/'` | Two listeners, owned by the container runtime | |
| 22 | Jellyfin and Grimmory are not reachable | `curl -m 3 -sS http://127.0.0.1:8096; curl -m 3 -sS http://127.0.0.1:6060` | Both fail ("connection refused") | |
| 23 | Databases are not reachable | `curl -m 3 -sS http://127.0.0.1:5432` | Fails | |
| 24 | API refuses anonymous access | `curl -sS -o /dev/null -w '%{http_code}\n' https://telos.test/api/v1/channels` | 401 | |
| 25 | Runtime hardening holds | `cd "$SRC" && sudo bash scripts/verify-runtime-hardening.sh --runtime --policy config/runtime-hardening.json` | "ok -- runtime: 12 containers inspected" | |
| 26 | Monitoring is healthy | `sudo bash scripts/verify-monitoring.sh --runtime` | "runtime ok" | |
| 27 | Containers run as non-root | `sudo podman inspect --format '{{.Name}} {{.Config.User}}' $(sudo podman ps -q) 2>/dev/null \| sort` | Eleven rows, none `root`: ClamAV `clamav`, client `101:101`, core `10001:10001`, egress proxy `proxy`, Grimmory and Jellyfin `1000:1000` (your uid), MariaDB `mysql`, PostgreSQL `postgres`, Prometheus `65534:65534`, Redis `redis`, Traefik `65532:65532` | |
| 28 | The known third-party findings are as documented | Read `documentation/security/known-vulnerabilities.md` | You understand what is open and why | |

## 10. Record the result

| | |
|---|---|
| Date and tester | |
| Commit tested (`git -C "$SRC" rev-parse HEAD`) | |
| `telos status` after first start | 12/12 up? |
| Sections 1–7 completed without deviation? | yes / no — deviations: |
| Section 8 rows passed / failed | passed: ___ failed: ___ (list numbers) |
| Section 9 rows passed / failed | passed: ___ failed: ___ |
| Anything surprising (errors, slow steps, confusing text) | |

For every failed row keep: the exact command or click, what you expected, what happened, and the output of
`sudo telos status` and `sudo podman logs --tail 50 telos-core`. Do not paste `node.env` contents anywhere.

## 11. Teardown (return the machine to how it was)

Do this when finished, or to start a fresh attempt. It **deletes the test node's data**. Only continue if you have no other Podman
workloads on this machine.

```bash
# 1. stop the stack (keeps data), then delete its containers, networks and volumes
sudo telos stop
sudo podman volume ls -q | grep '^telos_' | xargs -r sudo podman volume rm
sudo podman network ls -q | grep '^telos' | xargs -r sudo podman network rm
sudo podman rm -af          # only if Telos was your only use of Podman
sudo podman image prune -af # optional: frees the downloaded and built images

# 2. delete the node's files (the second and third lines delete the test media: skip them if you added your own)
sudo rm -rf /var/lib/telos /var/lib/telos-tls /opt/telos
sudo rm -rf /mnt/storage/shared && sudo rmdir /mnt/storage 2>/dev/null || true
sudo rm -f /usr/local/bin/telos

# 3. undo the system changes
sudo sed -i '/telos\.test/d' /etc/hosts
sudo sed -i '/^PODMAN_COMPOSE_PROVIDER=/d' /etc/environment
sudo rm -f /usr/local/share/ca-certificates/telos-test-ca.crt && sudo update-ca-certificates --fresh
sudo rm -rf /opt/telos-tools
rm -f /tmp/podman-compose.req ~/telos-test-ca.crt
# Firefox/Chrome: remove the "Telos Rehearsal CA" under the certificate Authorities list.

# 4. optional: remove the packages this runbook installed
sudo apt-get remove -y podman skopeo buildah ffmpeg && sudo apt-get autoremove -y
```

Check it is clean:

```bash
ls -d /opt/telos /var/lib/telos /var/lib/telos-tls /mnt/storage/shared 2>&1 | grep -vc "No such file"   # expect 0
getent hosts telos.test || echo "name removed"
ss -ltn | awk '$4 ~ /:(80|443)$/' | wc -l                                                              # expect 0
```

Bring the test VMs back if you want them: `~/.local/telos-vm/vm.sh start node; ~/.local/telos-vm/vm.sh start verifier`.

## 12. If something goes wrong

| Symptom | Likely cause | What to do |
|---|---|---|
| `install.sh --check` says Podman or podman-compose is missing | Section 2 not finished, or `PODMAN_COMPOSE_PROVIDER` not visible to `sudo` | Re-check `sudo env \| grep PODMAN`; use the `sudo env …` form |
| `telos doctor` lists a placeholder or an unreadable signing file | `init` did not complete | It names one repair per defect; never re-run `init` over an existing node |
| Browser says "unrecognized name" or the lock is missing while `status` is all healthy | Traefik cannot read the certificate (it runs as UID 65532) | `sudo ls -ld /var/lib/telos-tls; sudo ls -l /var/lib/telos-tls` — directory `root:65532 0750`, `server.key` `0640`; `telos start` also checks this |
| Browser certificate warning | CA not imported in that browser | Section 4; do not click through |
| `telos start` stops at "port 80 is taken" | Another web server on this machine | Stop it; Telos never falls back to other ports in production |
| Build fails while downloading | Network or registry hiccup | Re-run `sudo telos start --build`; it resumes from the cached layers |
| Machine very slow / containers killed | Not enough free memory | Confirm the VMs are stopped (section 1) |
| Gateway dependencies show Jellyfin `degraded` | Base URL not applied yet | `sudo telos stop && sudo telos start`; see `jellyfin_restart_pending` above |
| Other hosts cannot reach the node but `curl` on this machine works | A container bridge with IPv4 forwarding off (found and fixed on the reference VMs) | `sudo telos status` shows "forwarding OFF"; `sudo telos start` repairs it, or `sudo sysctl -w net.ipv4.conf.<bridge>.forwarding=1` |
| A secret you type contains `${` | Compose would expand it | Choose another value |
