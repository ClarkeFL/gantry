# gantry project instructions

## Writing style

**Never use em dashes (—) anywhere in this project.** Not in UI strings, code
comments, docs, README, commit messages, or chat replies about this project.
Rewrite with a comma, a period, parentheses, or a plain hyphen instead.

## UX conventions

- Cron schedules are never raw text inputs. Use the shared cron builder
  component (`web/src/lib/components/cron-input.svelte`) which offers
  preset frequencies (every N minutes, hourly, daily, weekly, monthly) with
  native time/day pickers, and a Custom mode for raw 5-field cron. It always
  shows the generated cron string and a human-readable summary.
- Assume the user does not know cron, dokku, or TLS. Every feature card gets
  a one-sentence plain-English description of what it does and why.
- Destructive actions use the in-app confirm dialog (`askConfirm`), never
  native `confirm()` (suppressed in the embedded browser).

## Build and release

- Frontend: `cd web && npm run build` must pass `npx svelte-check` with 0
  errors first; vite does not typecheck. Never ship with check errors.
- Local dev binary: `go build -o dist/gantry-dev .` (the agentide server
  config runs `dist/gantry-dev`, not the repo root).
- Release: commit on `dev`, ff-merge `main`, tag `vX.Y.Z`, push tag. CI
  builds `gantry-linux-{amd64,arm64}` release assets.
- Server (167.148.33.19, ssh alias `gantry-test`): update by downloading the
  latest release asset over ssh and restarting the `gantry` service.
