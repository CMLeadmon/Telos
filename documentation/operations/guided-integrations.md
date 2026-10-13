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
   After Jellyfin accepts the key and lists its libraries, the same call stores
   Jellyfin's **Base URL** as `/jellyfin` in its network settings (every other
   setting is sent back unchanged). The gateway reaches Jellyfin at
   `http://jellyfin:8096/jellyfin` and its HLS rewriting depends on that prefix;
   a fresh Jellyfin serves from `/`, so without this the gateway reports Jellyfin
   `degraded`. Jellyfin applies a new Base URL when it next starts, which the
   teardown in step 9 and the following `telos start` provide. Nothing is changed
   if the key is rejected or the libraries cannot be listed, and a Jellyfin that
   already serves under `/jellyfin` is left alone.
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

The Jellyfin discovery call (`GET /Library/VirtualFolders`), the Base URL save
(`GET`/`POST /System/Configuration/network`) and the Grimmory login and library
listing were exercised end to end on the Ubuntu 26.04 reference VM against the
locked Jellyfin 10.11.11 and Grimmory v3.5.0 images. The Grimmory listing
(`GET /api/v1/libraries`) is an array of objects with an integer `id` and a
`name` there, as the decoder expects, and the decoder still fails closed
(`grimmory_invalid_response`) on any other shape. That run is a rehearsal on a
virtual machine, not certification evidence, and the checked-in
`backend/testdata/integrations/grimmory-libraries.synthetic.json` remains
explicitly synthetic.
