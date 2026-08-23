# Configuration reference

Every setting this project reads, in one place: the credentials, the CLI flags
of every command, the GitLab CI/CD variables, and the knobs the end-to-end
suite honours.

Every entry below has been exercised against a real NetBox 4.6.7 — see
[How this was verified](#how-this-was-verified) at the end.

## How a setting is resolved

For the controller's configuration, later beats earlier:

1. a built-in default,
2. the `.env` file (or whatever `--config` names),
3. the process environment,
4. a command-line flag.

The file never overwrites something already exported, so a CI runner's
variables are not shadowed by a `.env` that happens to be checked out. Copy
[`.env.example`](../.env.example) to `.env` to start.

```bash
cp .env.example .env      # then fill in NETBOX_URL and NETBOX_TOKEN
```

## Credentials and connection

| Variable | Required | Default | What it does |
|---|---|---|---|
| `NETBOX_URL` | yes | — | Base URL of the NetBox instance, without a trailing `/api`. Missing it stops the run before any request. |
| `NETBOX_TOKEN` | yes | — | API token. A v1 token (40 hex characters) is sent as `Authorization: Token`, a v2 token (`nbt_<key>.<secret>`, NetBox 4.5+) as `Authorization: Bearer`. The format is detected from the prefix; nothing needs configuring. |
| `IGNORE_SSL_ERRORS` | no | unset (certificates **are** verified) | Skips TLS certificate verification, for a lab NetBox behind a self-signed certificate. Accepts `true`, `1`, `yes`, `on` (case-insensitive). Every run that sets it prints a warning naming the variable. |

> Certificate verification is on unless you turn it off. If you reach a NetBox
> whose certificate does not validate, the error says so — do not reach for
> `IGNORE_SSL_ERRORS` on a production instance to make it go away.

## `netbox-gitops`

### Choosing what runs

| Flag | Default | What it does |
|---|---|---|
| `--dry-run` | off | Plans without writing. Every run ends with `Plan: N to create, N to update, N to delete, N unchanged`. |
| `--only <phases>` | all phases | Comma-separated or repeated: `foundation`, `network`, `device-types`, `devices`, `virtualization`. Selected phases always run in dependency order regardless of the order you list them. An unknown phase is rejected by name. |
| `--site <slug>` | — | Restricts the **device and virtualization** phases to one site. Foundation and network objects are still reconciled in full. |
| `--device <name>` | — | Restricts device reconciliation to a single device. |
| `--vm <name>` | — | Restricts VM reconciliation to a single VM. |
| `--prune` | off | Deletes objects that carry the `gitops` tag but are no longer declared. Untagged objects are never touched. Refused in combination with `--site`/`--device`/`--vm`, because a filtered run cannot tell an out-of-scope object from an orphan. |

> Skipped phases are not validated. `--only devices` assumes the sites, roles
> and device types it references already exist in NetBox; it does not create
> them, and fails naming the first thing it could not resolve.

### Choosing what it reads

| Flag | Environment | Default | What it does |
|---|---|---|---|
| `--data-dir <dir>` | — | `.` | Directory holding `definitions/` and `inventory/`. **A directory you name explicitly must contain `definitions/`** — if it does not, the run fails rather than falling back, so a typo cannot apply the example dataset to your NetBox. The fallback to `example/` applies only when the flag is absent. |
| `--config <file>` | — | `.env` | `KEY=value` file read into the environment. `#` comments, blank lines, `export ` prefixes and quoted values are all understood. A missing default `.env` is fine; a missing file you named explicitly is an error. |
| `--devicetype-library <dir>` | `DEVICETYPE_LIBRARY` | `<data-dir>/definitions/device_type_library` | Root of a community-format device type library. The flag beats the environment, which beats the conventional path. A root that does not exist is skipped, not an error. |
| `--moduletype-library <dir>` | `MODULETYPE_LIBRARY` | `<data-dir>/definitions/module_type_library` | Same, for the `module-types/` tree. |
| `--ignore-file <glob>` | `IGNORED_FILES` (comma-separated) | `_*.yaml`, `_*.yml` | Filename globs to park. Matched against the filename only, not the path. Every skipped file is logged. An invalid glob fails at startup. |
| `--include-ignored-files` | — | off | Loads parked files anyway. |

### Reporting

| Flag | Default | What it does |
|---|---|---|
| `--output <format>` | `text` | `text` or `json`. `json` prints the plan to stdout and moves all logging to stderr, so the plan can be piped. Any other value is rejected. |
| `--detailed-exitcode` | off | Exit `0` when already in sync, `2` when changes are pending or were applied, `1` on error — the `terraform plan` convention, so a scheduled `--dry-run` becomes a drift monitor. |
| `--version` | — | Prints the version, commit and build date stamped at link time. |

### `netbox-gitops validate`

Checks the YAML against the live instance without writing: every value the
repository sets for a NetBox choice field is compared with what that instance
offers in its OPTIONS response, string lengths against the same source, and
references the repository does not declare itself are looked up.

```bash
./netbox-gitops validate                    # against $NETBOX_URL
./netbox-gitops validate --data-dir example
```

| Flag | Default | What it does |
|---|---|---|
| `--skip-references` | off | Checks values only, making no existence queries. For a large instance, or a token that may not read every endpoint. |

It inherits `--config`, `--data-dir`, `--devicetype-library`,
`--moduletype-library`, `--ignore-file` and `--include-ignored-files` from the
root command. Exit `0` when everything is accepted, `1` when it is not.

Where the three checks sit: `yamlcheck` needs no NetBox and answers "is this
repository coherent"; `validate` needs one and answers "will this instance
accept these values"; `--dry-run` needs one and answers "what would change".

## `yamlcheck`

Validates a data directory without contacting NetBox: YAML syntax, then the
typed models, then the cross-object checks in `pkg/lint`.

```bash
go run ./cmd/yamlcheck                  # definitions/, inventory/ and example/
go run ./cmd/yamlcheck path/to/data-dir # a directory holding definitions/ or inventory/
```

| Flag | Default | What it does |
|---|---|---|
| `-strict` | off | Treats warnings as failures. For a repository that declares everything it references. |
| `-allow-undeclared-refs` | off | Reports a reference to an object this repository does not declare as a warning instead of an error. For a partial adoption where some objects are managed elsewhere. |
| `-no-lint` | off | Syntax and model validation only, skipping the cross-object checks. |
| `-warnings` | `true` | `-warnings=false` prints errors only. |

The full list of checks is in the README section *Checking the YAML before it
reaches NetBox*.

## GitLab CI/CD variables

Set these under **Settings → CI/CD → Variables**, or per run under **Run
pipeline**. Two things are true of all of them:

- **Comparisons are case-sensitive and exact.** `RUN_TESTS="False"` does *not*
  skip tests, and `RUN_E2E="True"` does *not* enable the end-to-end job. Use
  lowercase `true`/`false`.
- **A project variable outranks anything in `.gitlab-ci.yml`**, job-level
  values included. That is why the `e2e` job pins its NetBox target with a
  shell `export` rather than a `variables:` entry: your project's `NETBOX_URL`
  would otherwise redirect a suite that deletes every managed object.

### What the pipeline needs from you

| Variable | Required for | Notes |
|---|---|---|
| `NETBOX_URL` | `go_validate`, `go_plan`, `go_apply` | The instance those jobs plan and apply against. |
| `NETBOX_TOKEN` | the same jobs | Mask it. Protect it if only protected branches should apply. |
| `GITLAB_RUNNER_TAG` | every job | Runner tag, default `docker`. Must be a plain value — a variable reference here cannot be expanded further. |

### Turning jobs on and off

| Variable | Default | Effect |
|---|---|---|
| `RUN_TESTS` | `true` | `false` skips `go_test`. |
| `RUN_QUALITY_CHECKS` | `true` | `false` skips `go_lint` and `yaml_check`. |
| `RUN_E2E` | `false` | Exactly `true` enables `e2e`. Needs a Docker-executor runner that can pull the three service images. |
| `COVERAGE_THRESHOLD` | `65` (from the Makefile) | Minimum total statement coverage. `go_test` runs `make coverage`, which measures `./...` and enforces the bar; setting this variable overrides the Makefile's default. A non-numeric or empty value fails the job rather than disabling the gate. |

### Images

| Variable | Default |
|---|---|
| `GOLANG_IMAGE` | `golang:1.24` |
| `NETBOX_IMAGE` | `netboxcommunity/netbox:v4.6-3.3.0` |
| `POSTGRES_IMAGE` | `postgres:16-alpine` |
| `REDIS_IMAGE` | `valkey/valkey:8-alpine` |

Mirror the last three into your own registry when running on-prem; they are
pulled only by the `e2e` job.

## End-to-end suite

Honoured by `tests/e2e/*.sh`, and by `make e2e`:

| Variable | Default | What it does |
|---|---|---|
| `NETBOX_URL`, `NETBOX_TOKEN` | — | Required. **Never point these at production**: the suite deletes every `gitops`-tagged object between seeds. |
| `E2E_SEEDS` | `1 2 3` | Space-separated generator seeds for `run.sh`. A failure reproduces from the same seed. |
| `E2E_KEEP` | `0` | `1` keeps the work directory for inspection. A failing run keeps it regardless and prints the path. |
| `E2E_WORK` | a temp dir | Where the binary, datasets and logs are written. |

`tests/e2e/provision-local.sh` builds a throwaway NetBox from source and takes
`NETBOX_VERSION` (default `v4.6.7`), `NETBOX_ROOT` (`/opt/netbox`),
`NETBOX_PORT` (`8000`) and `NETBOX_E2E_TOKEN`.

## GitHub Actions

`.gitlab-ci.yml` is the pipeline of record: it is the only one that plans and
applies against a real NetBox, and the only one that runs the end-to-end suite.

`.github/workflows/ci.yml` is the gate for wherever the code is reviewed. It
runs on every push and pull request and needs no configuration:
format/vet/licence headers, `make coverage` (the same target GitLab calls, so
the two cannot measure different things), `yamlcheck`, and a build. It never
touches a NetBox, so no credentials are involved.

## How this was verified

The tables above are not a description of intent. Each entry was exercised
against NetBox 4.6.7 built from source:

- **Credentials, `.env`, TLS, libraries, ignore patterns, filters, output,
  exit codes, prune** — driven through the built binary and checked against
  what NetBox actually held afterwards.
- **`yamlcheck` flags** — driven against fixture data directories, asserting
  exit codes and output.
- **GitLab rules** — read against GitLab's documented variable precedence, with
  the shell logic (the coverage gate) executed directly.
- **The properties the sample data relies on** — asserted by
  `tests/e2e/repo-data.sh` on every e2e run.

Unit tests cover the parsing and resolution rules that have edge cases:
`internal/dotenv` (quoting, comments, precedence), `resolveDataDir` (the
explicit-directory rule), and `pkg/lint`.
