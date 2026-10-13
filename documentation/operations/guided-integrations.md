# Guided Jellyfin and Grimmory setup

Telos does not create accounts or libraries inside Jellyfin or Grimmory: the
operator does that once, in each service's own interface, and then
`telos configure integrations` validates the result and records it. No script
creates a third-party account, and credentials never travel on a command line.

## What the command does

```text
sudo telos configure integrations --setup
```

1. Refuses to run while the production stack is up (cleanup must never stop
   somebody else's service) or when setup containers already exist.
2. Builds the gateway image so the validation container exists.
3. Starts only Jellyfin, Grimmory, its database and the controlled-egress proxy,
   with their administration UIs published on **127.0.0.1 only**
   (`docker-compose.setup.yml`; ports 8096 and 6060, overridable with
   `JELLYFIN_SETUP_PORT` / `GRIMMORY_SETUP_PORT`).
4. Prints the local URLs and an SSH tunnel for a remote host.
5. Waits while you finish setup in both UIs (below).
6. Reads the Jellyfin API key and the Grimmory gateway account **without echo**,
   and sends them (stdin of a throwaway `telos-core validate-integrations`
   container on the production networks) to the two services.
7. Shows each service's libraries as `name (id …)`, asks which to expose
   (comma-separated numbers; duplicates collapse; the result follows the order
   shown), validates again with the same credentials, and checks every selected
   ID still exists.
8. Writes `JELLYFIN_ADMIN_TOKEN`, `JELLYFIN_LIBRARY_IDS`, `GRIMMORY_ADMIN_USER`,
   `GRIMMORY_LIBRARY_IDS` (and `GRIMMORY_ADMIN_PASSWORD` only if you typed one) to
   `node.env` in a single atomic rewrite; every other byte is untouched and the
   mode stays `0600`.
9. Removes the setup containers (**volumes are kept**) and verifies both loopback
   ports are closed. This also runs on a failed validation, on Ctrl-C and on
   SIGTERM. If a port is still open the command fails and prints the exact
   command that removes the container publishing it.

Without `--setup` the upstream containers must already be running and the command
does not manage their lifecycle.

## Doing the manual part

Over SSH from your workstation:

```text
ssh -L 8096:127.0.0.1:8096 -L 6060:127.0.0.1:6060 <user>@<node>
```

Then browse to `http://127.0.0.1:8096` (Jellyfin) and `http://127.0.0.1:6060`
(Grimmory) on your workstation.

- **Jellyfin:** finish the first-run wizard, add your media libraries (the folders
  under `<STORAGE_PATH>/media`), then *Dashboard → API Keys* and create one for
  Telos. Paste it at the first prompt.
- **Grimmory:** create the administrator the gateway will use (default
  `telos-gateway`) and your libraries (books under `<STORAGE_PATH>/books`, drops
  under `<STORAGE_PATH>/bookdrop`).

## Values the gateway cannot carry

A secret containing the two-character sequence `${` is refused: Compose providers
expand it even inside quotes, so the gateway would receive a different value.
Choose another password or key.

## Re-running and rotating

Run the command again at any time to rotate the Jellyfin key or Grimmory password
or change the exposed libraries: a blank Grimmory password keeps the stored one.
A failed run never changes `node.env`. Restart with `sudo telos stop` and
`sudo telos start` to apply the new values.

## Verification status

The Jellyfin discovery call (`GET /Library/VirtualFolders`) and the Grimmory
login follow the services' documented APIs. The Grimmory library listing
(`GET /api/v1/libraries`, an array of objects with `id` and `name`) follows the
Phase 2 contract but has **not** been captured from a live pinned Grimmory in this
repository: `backend/testdata/integrations/grimmory-libraries.synthetic.json` is
explicitly synthetic and the decoder fails closed (`grimmory_invalid_response`) on
any other shape. Until a sanitized capture from the locked Grimmory version
replaces that fixture, live Library discovery is unverified.
