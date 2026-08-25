# Configuration reference

Every setting this project reads, in one place: the credentials, the CLI flags
of every command, the collector source configuration, the GitLab CI/CD
variables, and the knobs the end-to-end suite honours.

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

`collect` and `ingest` read none of the three. They never contact NetBox; the
credentials they need are the read-only ones for the systems being scanned, and
those are named in [`collectors.yaml`](#collectorsyaml).

## Presentation (all commands)

These are persistent flags, resolved once before any subcommand runs, so they
apply everywhere.

| Flag | Environment | Default | What it does |
|---|---|---|---|
| `--log-format <format>` | `LOG_FORMAT` | `text` | `text` or `json`. `json` renders every log line as a `{"level","msg"}` object. |
| `--no-color` | `NO_COLOR` (any non-empty value) | colour on a TTY | Disables ANSI colour. Colour is auto-disabled when stdout is not a TTY regardless. |

Exit codes are uniform across every command: `0` success (or nothing pending),
`1` error, `2` changes pending (`--detailed-exitcode`, `import --diff`), `3` a
safety guard refused the run (`--assert-site`, a rewrite guard).

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
| `--adopt` | off | First-contact mode: an existing object receives only the managed tag, no other field is written. Creates are unaffected. Use for the first sync after `import` over a populated NetBox. Composes with `--assert-site`. |
| `--assert-site <slug>` | — | Destination guard. Comma-separated or repeated. Aborts with exit `3` before any write that would land — or the existing object already sits — outside these sites. Site-less shared objects (tags, roles, device types) are allowed and logged. Refused with `--prune`. Works under `--dry-run`. |

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

### `netbox-gitops collect` and `netbox-gitops ingest`

Two front doors on one pipeline. `collect` fetches a snapshot from the sources
in `collectors.yaml`; `ingest` is handed one another tool produced. Both then
match it against the declared inventory, write YAML and print the same summary.

**Neither talks to NetBox.** They need no `NETBOX_URL` and no `NETBOX_TOKEN`;
their only effect is changed files in this repository. See
[`INGEST.md`](INGEST.md).

```bash
./netbox-gitops collect --dry-run                              # print the diffs, write nothing
./netbox-gitops collect --source pve-prod
./netbox-gitops ingest --format idrac-json --input scan.json
```

Shared by both:

| Flag | Default | What it does |
|---|---|---|
| `--dry-run` | off | Prints a unified diff of every file that would change and writes nothing — not even the `inventory/discovered/` directory. |
| `--output <format>` | `text` | `text` or `json`. `json` prints the change list (summary, per-file action, the keys written, the parked files) to stdout and moves all logging to stderr. Any other value is rejected. |
| `--detailed-exitcode` | off | Exit `2` when the repository would change, `0` when it already says what the scan found, `1` on error. This is how a scheduled CI job decides whether to open a merge request. It reports the change under `--dry-run` too. |
| `--custom-field-prefix <prefix>` | `hw_` | The prefix of the custom fields a scan owns. Nothing outside it is ever created, updated or reordered, so a field another team maintains on the same device is untouched. |

`collect` only:

| Flag | Environment | Default | What it does |
|---|---|---|---|
| `--collectors-config <file>` | `COLLECTORS_CONFIG` | `<data-dir>/collectors.yaml` | The source configuration. The flag beats the environment, which beats the conventional path. A missing file is an error naming the path, not a run that scans nothing. |
| `--source <name>` | — | every configured source | Comma-separated or repeated. A name that matches nothing is an error listing what *is* configured. Each source is planned and applied on its own, so one unreachable source does not discard what the others found — the run still exits non-zero. |

`ingest` only:

| Flag | Default | What it does |
|---|---|---|
| `--format <format>` | — | **Required.** Currently `idrac-json`: the document `idrac-inventory -output json` writes. Anything else is rejected by name. |
| `--input <file>` | — | **Required.** The document to read; `-` reads standard input. |
| `--source <name>` | the format's own (`idrac`) | The source name recorded on the snapshot. It names the directory generated files land in, so two scans (one per datacentre, say) can be kept apart. |

Both inherit `--config`, `--data-dir`, `--ignore-file` and
`--include-ignored-files` from the root command. `--ignore-file` affects only
*which files the reconciler would apply*: ingest always reads parked files, or
it would generate a second copy of a machine somebody has already started
documenting.

### `collectors.yaml`

Read by `collect`. Each entry under `sources:` configures one source. No secret
is ever written here — the file names the *environment variable* holding the
token — so it is safe to commit. A key this parser does not recognise is an
error naming the key: a mistyped `verify_tsl: false` that was silently dropped
would leave you believing verification is off when it is on.

```yaml
sources:
  - name: pve-prod
    type: proxmox
    url: https://pve.example.com:8006
    token_env: PROXMOX_TOKEN
    verify_tls: true
    cluster: berlin-prod-cluster
```

| Key | Required | Default | What it does |
|---|---|---|---|
| `name` | yes | — | Identifies the source in logs, in `--source`, in the run summary and in the path of any file generated from it. Must be unique and must not contain a path separator. |
| `type` | yes | — | Selects the collector. Currently only `proxmox`. An unknown type is an error listing the ones that exist. |
| `url` | yes | — | The source's API base URL, e.g. `https://pve.example.com:8006`. |
| `token_env` | yes | — | The **name** of the environment variable holding the token, never the token. For Proxmox that variable holds `user@realm!tokenid=secret`; `PVEAuditor` on `/` is enough, since every request is a GET. An unset variable is an error naming the variable. |
| `verify_tls` | no | `true` | `false` skips TLS certificate verification for this source only, and logs a warning naming the source on every run — the same bargain `IGNORE_SSL_ERRORS` strikes for the NetBox client. |
| `timeout_seconds` | no | `30` | Bounds a single API request. A whole scan may take longer: one request per guest is made to read its NICs. |
| `cluster` | no | — | The NetBox cluster the source's guests belong to. Proxmox reports no name a NetBox cluster can be matched on, so it is declared rather than guessed. Guests from a source without one are written to a *parked* file, since a VM needs a cluster or a site. |

## `netbox-gitops import`

The reverse sync: reads a live NetBox and writes native YAML. Read-only (GETs
and OPTIONS only). Every flag has an environment variable **except** the rewrite
flags, which are flag-only on purpose (a stray `REWRITE_*` in CI variables would
silently rewrite every import).

| Flag | Environment | Default | What it does |
|---|---|---|---|
| `--data-dir <dir>` | — | `.` | Where to write. Refuses a non-empty directory unless `--force`. |
| `--force` | `IMPORT_FORCE` | off | Write into a non-empty directory. |
| `--dry-run` | — | off | List the files that would be written and print the report; write nothing. |
| `--diff <dir>` | — | — | Diff the import against an existing repo, write nothing, exit `2` on drift. |
| `--only <phases>` | `IMPORT_ONLY` | all | Comma-separated or repeated: `foundation`, `network`, `device-types`, `devices`, `virtualization`. |
| `--site <slug>` | `IMPORT_SITES` | — | Restrict site-scoped source objects. Does **not** filter site-less IPAM (VRFs, unassigned addresses, non-site-scoped prefixes) — those are counted in the report. |
| `--tag <slug>` | `IMPORT_TAGS` | — | Only import objects carrying these tags. |
| `--exclude-tag <slug>` | `IMPORT_EXCLUDE_TAGS` | — | Skip objects carrying these tags. |
| `--exclude-site <slug>` | `IMPORT_EXCLUDE_SITES` | — | Skip site-scoped objects in these sites — e.g. a leftover sandbox from a rehearsal. |
| `--managed-only` | `IMPORT_MANAGED_ONLY` | off | Only import objects already carrying the `gitops` tag. |
| `--split-by <mode>` | `IMPORT_SPLIT_BY` | `site` | Partition inventory into files: `site`, `rack`, `role`, `none`. |
| `--defaults` / `--no-defaults` | `IMPORT_DEFAULTS` | on | Hoist fields shared across a file into a `defaults:` block. |
| `--defaults-min-items <n>` | `IMPORT_DEFAULTS_MIN_ITEMS` | `3` | Fewest items in a file before any key is hoisted. |
| `--report <file>` | `IMPORT_REPORT` | `IMPORT-REPORT.md` | Coverage report path; `-` writes it to stderr only. |
| `--fail-on-gaps` | `IMPORT_FAIL_ON_GAPS` | off | Exit non-zero if the report lists any skipped object. |
| `--output <format>` | `IMPORT_OUTPUT` | `text` | `text` or `json` (file list + report summary on stdout, logs on stderr). |

### Sandbox rewrite (flag-only — never read from the environment)

Rehearse an adoption onto a scratch site without touching production. See
[IMPORT.md](IMPORT.md).

| Flag | Default | What it does |
|---|---|---|
| `--rewrite-site <OLD=NEW>` | — | Rewrite site slugs; repeatable; `*=NEW` maps every site. Requires `--name-prefix` (or `--no-name-prefix`), and `--rewrite-vrf` when the network phase runs. |
| `--rewrite-vrf <name>` | — | Put all imported IPAM into this scratch VRF (created with `enforce_unique`), so rewritten prefixes/addresses cannot match production. |
| `--name-prefix <str>` | — | Prefix every name-identified object (devices, VMs, racks, clusters). |
| `--no-name-prefix` | off | Allow `--rewrite-site` without a name prefix (you accept the collision risk). |

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
| `RUN_QUALITY_CHECKS` | `true` | `false` skips `go_lint`, `yaml_check` and `ingest_check`. |
| `RUN_E2E` | `false` | Exactly `true` enables `e2e`. Needs a Docker-executor runner that can pull the three service images. |
| `COVERAGE_THRESHOLD` | `65` (from the Makefile) | Minimum total statement coverage. `go_test` runs `make coverage`, which measures `./...` and enforces the bar; setting this variable overrides the Makefile's default. A non-numeric or empty value fails the job rather than disabling the gate. |

### Fact ingestion (optional, opt-in)

The scheduled jobs that scan sources and open merge requests are **not** part
of the pipeline until you include
[`.gitlab-ci.ingest.example.yml`](../.gitlab-ci.ingest.example.yml). A job that
opens merge requests on a schedule should be a decision somebody made rather
than a default they inherited.

| Variable | Default | Effect |
|---|---|---|
| `INGEST_SCHEDULE` | unset | Exactly `true`, set on a **pipeline schedule**, is what makes the ingestion jobs run automatically. Without it they are manual only. |
| `INGEST_BRANCH` | `chore/ingest-facts` | The branch the jobs push to and open the merge request from. One branch, reused. |
| `GITOPS_PUSH_TOKEN` | — | A project access token with `write_repository` and `api` scope, so a job can push a branch and open a merge request. Mask it. It is the only write credential these jobs hold — **none of them holds a NetBox token**. |
| `PROXMOX_TOKEN` | — | Read by name from `collectors.yaml` (`token_env`). `user@realm!tokenid=secret`; `PVEAuditor` on `/` is enough. Mask it. |
| `IDRAC_USERNAME`, `IDRAC_PASSWORD` | — | Read-only iDRAC credentials for the importer container. Mask the password. |
| `IDRAC_IMPORTER_IMAGE` | `registry.example.com/idrac-netbox-importer:latest` | The sibling importer's image. Mirror it into your own registry when running on-prem. |

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
- **`collect`/`ingest` flags and `collectors.yaml`** — driven against fixture
  data directories and a recorded Proxmox API, asserting the files written, the
  exit codes and that `--dry-run` writes nothing.
- **GitLab rules** — read against GitLab's documented variable precedence, with
  the shell logic (the coverage gate) executed directly.
- **The properties the sample data relies on** — asserted by
  `tests/e2e/repo-data.sh` on every e2e run.
- **The whole ingestion path** — asserted by `tests/e2e/ingest.sh` on every
  branch, against a fake Proxmox and a recorded importer document.

Unit tests cover the parsing and resolution rules that have edge cases:
`internal/dotenv` (quoting, comments, precedence), `resolveDataDir` (the
explicit-directory rule), and `pkg/lint`.
