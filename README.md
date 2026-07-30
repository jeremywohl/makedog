# makedog

A runner for your server binaries.

- **Restarts on builds** — watches the compiled binary, not your source. Recompiling is your editor's or your agent's job; makedog notices the new build and bounces the process.
- **Logs every run** — each execution is recorded to a durable store: replay it, tail it, diff two runs, grep the last week.
- **Live everywhere** — other shells and agents watch in real time: `tail` follows output across restarts, `current` gates on the run of the build on disk, `next` catches the coming run from its first line, `--follow` streams one in flight.
- **Sends signals** — from an interactive menu, or remotely from another terminal.
- **Runs make targets** — pick a Makefile target from within the watch session.
- **Remote control** — every instance listens on a unix socket: `status`, `restart`, `stop`, `start`, `signal` from anywhere in the project.
- **Agent-friendly** — `--json` output, and readiness gates like `current --until 'listening' --timeout 30s` with meaningful exit codes.

## Examples

Supervise a server; rebuilds restart it automatically:

```console
$ makedog bin/server
keys: 'c' to clear screen, 'h' for this help, 'k' to mark log, 'm' to run make, 'q' to quit, 's' to send signal, 't' to run make targets, 'x' to start/stop

--> start 1 bin/server (pid 30934, hash 7fbd0d4)
[2026-07-23 16:07:35.722]  listening on :8080
[2026-07-23 16:07:36.723]  GET /healthz 200 0.4ms
[2026-07-23 16:07:37.724]  GET /api/items 200 12.1ms
[2026-07-23 16:12:27.493]  BLE central state changed: PoweredOn
[2026-07-23 16:12:27.493]  [SensorManager] BLE ready, loading sensors...
[2026-07-23 16:12:27.496]  [SensorManager] Loaded 0 active sensors
[2026-07-23 16:12:27.496]  Starting BLE scan (service: FEBC)
--> stop  1 bin/server [4MB memory, 2s cpu time, 5m wall time]

* binary modified at 16:12:39.541

--> start 2 bin/server (pid 30965, hash a80f21e)
[2026-07-23 16:12:40.146]  listening on :8080
```

While it runs, single keys drive it: `r` restart, `x` stop/start, `s` signal menu, `t` make-target menu, `k` mark the log, `q` quit.

Read back what happened:

```console
$ makedog runs                 # list recorded runs
$ makedog latest               # replay the latest run (possibly live)
$ makedog latest~1 --plain     # the run before, ANSI stripped
$ makedog current              # the run of the build on disk, waiting if needed
$ makedog diff                 # what changed between the last two runs?
$ makedog search 'ERROR|panic' --since 2d
```

Gate on readiness — after a rebuild, wait for the run of the binary now on disk to say it's up:

```console
$ make && makedog current --until 'listening on' --timeout 30s
```

Exit 0 when the line matches, 1 if the run ends first, 2 on timeout. `current` keys on the binary's hash, so it works called at any point after the build: it returns at once when that run is already up (even if a no-op build restarted nothing), and waits for the restart otherwise. (`next` remains the pure event form — wait for whatever run starts next.)

Poke the live process from another terminal:

```console
$ makedog status
$ makedog restart
$ makedog signal HUP
```

See the manpage for more details.

## Install

```console
$ brew install jeremywohl/tap/makedog
```

Or build from source (requires Go):

```console
$ git clone https://github.com/jeremywohl/makedog && cd makedog && make
```

The binary lands in `bin/makedog`.

## Tell your agent about it

Paste this into your `CLAUDE.md` / `AGENTS.md` and your agent can rebuild, confirm the restart, and read the logs, without holding the server process itself:

```markdown
## Running the server

The server runs under makedog, which restarts it whenever the binary
is rebuilt. Every run's output and status are logged and readable in
real time; never start, stop, or kill the server process yourself.

Example invocations:

- Print output so far from the run of the build on disk,
  waiting for it to start if needed: `makedog current`, often paired
  with options `--until '<regex to stop reading at>'` (implies --follow),
  and `--timeout 30s` (exit 0 = matched, 1 = run ended first, 2 = timeout).
  Run any time after builds, e.g. `make && makedog current <opts>`.
- Check the current run: `makedog info --json`, showing pid, binary
  hash, git state, start time, live or exited. This answers "is it
  up now?"; `current` (with --until) answers "tell me when this
  build is up."
- Print the last/live run immediately: `makedog latest`.
- Other options for current/latest/next: `--follow` streams until end of run,
  `-n 100` and `--since 5m` trim, `--json` emits JSONL records, `--plain`
  strips ANSI.
- Dig through history: `makedog search '<regex>' --since 2d`; compare
  two runs with `makedog diff`.
- Human-driven, rarely for agents (rebuilds already restart the server):
  `makedog restart | stop | start | status`. `makedog signal <SIG>`
  delivers whatever the app wired that signal to — know the handler
  before sending.
- See `makedog --help` for more details.
```

## Run logs

Every run is recorded as JSONL — one record per output line, plus lifecycle events (starts, stops, signals, marks, make invocations) — under `~/.local/state/makedog` (override with `MAKEDOG_STATE_DIR`).

Read them with:

| verb | does |
|---|---|
| `runs` | list recorded runs (`--long` for full metadata cards) |
| `<run#>`, `latest[~N]` | replay a run (`--follow` to stream a live one) |
| `current` | the run of the binary as built on disk, waiting for it if needed |
| `next` | wait for the next run and stream it from its start |
| `tail` | follow live output across restarts, endlessly |
| `search <regex>` | grep recorded runs; `--runs 30..34`, `--since 6h`, `--all-binaries` |
| `info [ref]` | one run's metadata card |
| `diff [a [b]]` | unified diff of two runs' output |
| `path [ref]` | the log's file path |

All readers take `--json` for raw records, `--plain` to strip ANSI, `--binary` to pick among a project's binaries, and `-C dir` to reach another project.

Older logs are compressed in the background (zstd, after 24h idle) and pruned by count, age, or total size per the retention policy. The newest two runs are always left untouched.

## Signals

`s` opens a signal menu with the common set. Config can scope and name each in your project:

```toml
[[signals]]
signal = "HUP"
name   = "reload config"

[[signals]]
signal = "USR1"
name   = "rotate logs"
```

Configured signals replace the default menu — your project's menu shows exactly these. The remote verb is unconstrained: `makedog signal USR2` sends anything makedog knows.

## Remote control

Each watching instance registers on a unix socket, so from any terminal in the project:

```console
$ makedog status               # who's live?
$ makedog stop                 # park the binary (makedog keeps watching)
$ makedog start
$ makedog restart
$ makedog signal TERM
```

With several instances live, mutating verbs ask for `--instance <pid>` rather than guessing.

## Configuration

makedog loads `.makedog.toml` from the project directory (or `--config path`). Missing is fine; malformed warns and runs with defaults.

```toml
[[signals]]                # scope and name the signal menu
signal = "HUP"
name   = "reload config"

[logs]                     # retention; zero disables a rule
compress_after = "24h"     # default
keep_runs      = 500       # default
max_age        = "90d"     # default: never expire
max_total      = "512MB"   # default: unlimited
```

