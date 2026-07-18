# gantry

A single-binary panel for [Dokku](https://dokku.com). ~25MB RAM, no database, no runtime
dependencies — Dokku and the filesystem are the state.

- **Apps** grouped into categories, with status, env editor, log streaming, deploy trigger
- **Per-app cron tab** (the reason this exists): jobs run in a fresh one-off container
  (`dokku --rm run <app> <cmd>`) via `/etc/cron.d`, 0MB between runs, editable live,
  last-run time + exit code shown per job
- **Databases**: dokku service plugins (postgres, mysql, redis, …) listed with status
- **Auth**: single admin, argon2 password + TOTP 2FA (Google Authenticator etc.), rate-limited
- **Self-update**: one button pulls the latest release binary and restarts via systemd

## Install (Ubuntu/Debian, as root)

```sh
curl -fsSL https://raw.githubusercontent.com/ClarkeFL/gantry/main/install.sh | sudo GANTRY_REPO=ClarkeFL/gantry sh
```

What it does, in order:

1. Installs Dokku (official bootstrap) — skipped if `dokku` is already on the box
2. Downloads the latest `gantry-linux-<arch>` release binary to `/usr/local/bin/gantry`
3. Runs `gantry init` (interactive, once): asks for the admin password, generates the 2FA
   secret and prints the `otpauth://` URI — add it to Google Authenticator / 1Password now,
   it is only shown here
4. Installs a systemd service: starts on boot, restarts on crash (and after self-update)

Then open `http://<server-ip>:8022`. Re-running the installer is safe — it updates the
binary and skips setup.

Lost the 2FA secret or password? `rm /var/lib/gantry/auth.json && gantry init && systemctl restart gantry`.

## Dev

```sh
cd web && npm install && cd ..
printf 'devpassword1\n' | GANTRY_DATA=./data go run . init
cd web && npm run build && cd ..           # go:embed needs web/build to exist
GANTRY_MOCK=1 GANTRY_DATA=./data GANTRY_CRON_DIR=./data/cron.d go run .
```

`GANTRY_MOCK=1` fakes dokku so the UI works on any machine. For frontend iteration,
`cd web && npm run dev` proxies `/api` to :8022.

## Releasing a new version

Releases are built by GitHub Actions (`.github/workflows/release.yml`), triggered by
pushing a tag that starts with `v`:

```sh
git tag v0.2.0
git push origin v0.2.0
```

That workflow builds the frontend, compiles `gantry-linux-amd64` and `gantry-linux-arm64`
(version stamped from the tag), and attaches both to a GitHub release. Watch it with
`gh run watch`.

Once the release is up, every installed panel can pull it with the **Update panel** button
in the sidebar (downloads the latest release binary, swaps itself, restarts via systemd —
takes ~5 seconds). `install.sh` always grabs the latest release too.

Rollback: `gh release delete <bad-tag>` so `latest` points at the previous release again,
then hit Update on the server.

## Env

| var | default | |
|---|---|---|
| `GANTRY_ADDR` | `:8022` | listen address |
| `GANTRY_DATA` | `/var/lib/gantry` | auth.json, meta.json, cron logs |
| `GANTRY_CRON_DIR` | `/etc/cron.d` | where cron files are written |
| `GANTRY_REPO` | — | `user/repo` for self-update |
| `GANTRY_MOCK` | — | `1` = fake dokku for UI dev |
