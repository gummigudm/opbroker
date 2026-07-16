# opbroker

A 1Password credential broker with an in-memory caching agent.

`opbroker` wraps arbitrary CLI tools with credentials fetched from 1Password. A local agent caches resolved credentials in memory (TTL) so repeated invocations don't pay the `op` CLI cost (~300-500ms) on every call.

---

## Requirements

- macOS on Apple Silicon (peer verification uses macOS-specific APIs)
- [1Password CLI](https://developer.1password.com/docs/cli/) (`op`) 2.x, signed in

## Install

```sh
curl -fsSL https://github.com/gummigudm/opbroker/releases/latest/download/install.sh | bash
```

The installer downloads the binary + SHA256SUMS, verifies the checksum, and installs to `$GDOT_DIR/bin` if set, otherwise `~/.local/bin`. Add that directory to your `PATH` if it isn't already.

To install a specific version:

```sh
curl -fsSL https://github.com/gummigudm/opbroker/releases/download/v0.1.0/install.sh | bash
```

## Quickstart

1. Run `opbroker` once. On first invocation it walks you through picking a 1Password account and writes `~/.opbroker/config.yaml` plus an empty `~/.opbroker/profiles.yaml`.
2. Add a profile to `~/.opbroker/profiles.yaml` (see [Configuration](#configuration) below).
3. Run a wrapped command:

   ```sh
   opbroker run --profile foo -- foo.sh --some-arg
   ```

The agent auto-starts on first use. First-time setup is triggered by any user-facing subcommand (`opbroker`, `opbroker run`, `opbroker session start`). Diagnostic commands (`session stop|refresh|status`) never trigger setup — they return a friendly "not configured" error instead, so they stay scriptable.

## Configuration

### `~/.opbroker/config.yaml`

```yaml
op_account: YOUR_ACCOUNT_ID

agent:
  ttl: 30m
  socket: ~/.opbroker/run/agent.sock
  allowed_callers:
    - /path/to/opbroker
```

`allowed_callers` is populated automatically on first-time setup with the currently-running binary's canonical path. If you move the binary, update this list — the agent verifies each connecting peer's executable path against it.

### `~/.opbroker/profiles.yaml`

```yaml
profiles:
  foo:
    tag: FooService/creds
    account_field: account        # or "title" to use the item's title as the account identifier
    command: /path/to/foo.sh
    env:
      FOO_TOKEN: foo_token
    args:                         # optional: inject resolved values as CLI flags
      --account: ${account}       # ${account} = resolved account name (also the extraction target)
    # arg_style: separate         # "separate" (default) writes `--flag value`; "equals" writes `--flag=value`
    # arg_placement: first        # "first" (default) prepends; "last" appends
```

**`env` map** — Each entry projects a 1Password field value into a target-process environment variable.

**`args` map** — Each entry maps a target-command flag to a value source:

- `${account}` — the resolved account name. If the user passes this flag in their command (e.g. `foo --account acct1`), opbroker extracts it and uses it to select the account. Otherwise opbroker resolves the account (via last-selection or picker) and *injects* the flag into the target argv.
- `${title}` — the 1Password item's title.
- Any other string — a 1P field name; the resolved value is injected as the flag's value. If the source field is CONCEALED in 1Password, opbroker marks it as secret for masking in debug output.

Flags the user already supplied are never duplicated; their explicit value wins.

## Commands

```
opbroker                              # first-run setup (if needed) then prints usage
opbroker run [flags] -- <cmd> [args]  # run wrapped command
opbroker session start [--background]
opbroker session stop
opbroker session refresh              # clear caches
opbroker session status
opbroker --version
```

### `opbroker run` flags

- `--profile, -p NAME` — named profile from `profiles.yaml`
- `--tag TAG` — 1P tag filter (overrides profile)
- `--account-field FIELD` — item field identifying the account (overrides profile)
- `--account NAME` — pre-select account, skip picker
- `--op-account UUID` — 1Password account UUID override
- `--field ENV=field_name` — repeatable env→field map (used without `--profile`)

### Target-argv flags interpreted by opbroker

Placed inside the target's argv (after `--`), stripped before exec:

- `--opbroker-debug` — see [Debugging](#debugging).

## Shell wrapper example

Define a shell function that always routes a command through `opbroker`:

```zsh
foo() {
    opbroker run --profile foo -- foo.sh "$@"
}
```

Now `foo <args>` fetches credentials from 1Password (or the cache) and runs `foo.sh <args>` with the resolved env vars and injected flags.

If the profile matches multiple 1Password items, an interactive picker prompts you to choose on first use; the selection is remembered for the agent's lifetime. To skip the picker in scripts, pass `--account` to `opbroker` directly:

```sh
opbroker run --profile foo --account account1 -- foo.sh
```

## Debugging

Pass `--opbroker-debug` in the target's argv to see what opbroker *would* execute — env vars, resolved args, final argv — without actually running the target. Secrets sourced from CONCEALED 1Password fields are masked as `<masked>`.

```
$ foo --opbroker-debug --account account1
opbroker wrapped:
  environment:
    FOO_TOKEN: <masked>
  command:
    /path/to/foo.sh --account account1

(--opbroker-debug set; target not executed)
(<masked> = value sourced from a CONCEALED 1Password field)
```

The flag is stripped from the argv before extraction/injection, so it never reaches the target. Resolution still happens — this is a truthful preview, not an offline mode.

## Troubleshooting

- **`unauthorized caller`** — your `opbroker` binary path isn't in `allowed_callers`. Update `~/.opbroker/config.yaml`.
- **`agent did not become ready within 3s`** — the agent failed to launch. Try `opbroker session start` (foreground) to see the error.
- **Picker fails with `no controlling terminal`** — you're running without a tty. Pass `--account` to select non-interactively.
- **`opbroker is not configured yet`** — first-run setup hasn't happened. Run `opbroker` in an interactive shell to trigger it.

---

## Development

Requirements for building/testing locally:

- Go 1.22+
- [Task](https://taskfile.dev/) (recommended, but not required)
- `golangci-lint` (optional; only needed for `task lint`)

### Common tasks

```sh
task test        # go vet + go test + golangci-lint (if installed)
task build       # build ./dist/opbroker for current platform
task install     # build and copy to $GDOT_DIR/bin or ~/.local/bin
task lint        # run golangci-lint
task fmt         # gofmt -w
task fmt:check   # fail on unformatted files
task dev:reload  # build + install + kill any running agent (dev loop)
```

The underlying scripts are also callable directly:

```sh
./tests/run.sh
./.repo/scripts/build.sh
```

### Repo layout

- `cmd/opbroker/` — entry point and subcommand dispatch
- `internal/agent/` — socket server, cache, client, protocol
- `internal/argparse/` — argv extract + inject helpers
- `internal/config/` — YAML config loading and validation
- `internal/opcli/` — `op` CLI wrapper + fetcher adapter
- `internal/selector/` — bubbletea account picker
- `internal/security/` — macOS peer verification (cgo)
- `internal/setup/` — first-run interactive setup
- `internal/version/` — build-time version string
- `.repo/` — linter config, build/install scripts, release-please config
- `tests/` — test entry point + probe binary for negative security tests
- `.github/workflows/` — CI + release automation
