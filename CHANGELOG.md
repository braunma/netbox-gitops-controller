# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Because this controller can **delete objects from a live NetBox** when run with
`--prune`, treat this file as an upgrade safety document rather than
bookkeeping: read the entries between the version you last ran and the one you
are about to run before applying a sync with pruning enabled.

## [Unreleased]

### Removed

- **BREAKING: the Proxmox provisioning path is gone.** `cmd/tfgen`, `pkg/tfgen`,
  the `terraform/` module and the `tf_*` CI jobs are removed, and with them the
  `provision:` key on a virtual machine. The controller no longer creates
  anything on a hypervisor; it documents infrastructure in NetBox, and nothing
  else.

  The direction is now reversed. Instead of YAML being rendered outward into
  Terraform variables that build VMs, facts discovered *about* running
  infrastructure are written inward into this repository's YAML, reviewed as a
  merge request, and become NetBox truth through the same reconcile pipeline as
  every hand-written line. See [`docs/INGEST.md`](docs/INGEST.md).

  **What you have to do.** Delete `provision:` from every VM file. A file that
  still carries it fails validation rather than being quietly ignored:

  ```
  ✗ virtual machine web-01: provision: the Proxmox provisioning path
    (cmd/tfgen, terraform/) was removed and this key no longer does anything —
    delete it; see CHANGELOG.md
  ```

  A key that no longer does anything is worse than an error, because the author
  goes on believing something is still being built somewhere.

  `vmid`, `vm_template_id` and `node` are kept and unchanged: they describe
  where a VM lives on its hypervisor, the first two are stored in NetBox as
  custom fields, and `vmid` is one of the keys a collector may now write.
  The per-environment layout under `inventory/virtual/<env>/` is kept as well —
  it is a directory convention, not a Terraform state boundary.

  The CI variables `ENABLE_PROXMOX`, `TF_ALLOW_DESTROY`, `OPENTOFU_IMAGE` and
  every `TF_VAR_*` are no longer read by anything; remove them from your
  project settings.

### Added

- **`netbox-gitops validate`: check the YAML against the NetBox that will
  receive it, without writing.** The typed models carry a hardcoded copy of
  NetBox's choice sets, which can only ever approximate a particular instance —
  the values it accepts depend on its release and its plugins, and some sets are
  far too large to keep by hand (NetBox 4.6 offers 216 interface types, so
  `type` was not validated at all). A value the models let through became a
  `400` partway through an apply, after earlier objects were already written.

  `validate` reads each endpoint's `OPTIONS` response — the authority on what
  that server accepts — and checks every choice value and string length the
  repository declares against it, naming the near miss where there is one:

  ```
  ✗ device srv-01 interface eth0: type "25gbase-x-sfp29" is not a value this
    NetBox accepts; did you mean "25gbase-x-sfp28"? (invalid-choice)
  ```

  References the repository does not declare itself are looked up in NetBox, so
  a site that exists only on the server is accepted and one that exists nowhere
  is reported. Everything wrong is reported at once rather than stopping at the
  first rejection, one schema is fetched per endpoint rather than per object,
  and identical references are looked up once. `--skip-references` limits it to
  values. Every request it makes is a read, and the client runs in dry-run mode
  throughout, so the path cannot write even if asked to.

  The three checks now answer three different questions: `yamlcheck` (no NetBox
  needed) whether the repository is coherent with itself, `validate` whether
  this instance accepts these values, `--dry-run` what would change.

- **`docs/CONFIGURATION.md`: every setting in one verified reference.** Each
  environment variable, each flag of `netbox-gitops`, `yamlcheck` and `tfgen`,
  each GitLab CI/CD variable and each end-to-end knob, with its default and
  what it actually does — checked against a NetBox 4.6.7 rather than described
  from intent. It also states the two rules that were previously folklore: how
  a setting is resolved (default → `.env` → environment → flag) and that a
  GitLab **project** variable outranks anything `.gitlab-ci.yml` says.

- **The `.env` file the README always documented is now actually read.**
  `--config` existed as a flag, defaulted to `.env`, and appeared in `--help`
  — but nothing in the program ever opened it: credentials came from the
  environment only. Following the README ("Create a `.env` file in the root
  directory") therefore produced `NETBOX_URL and NETBOX_TOKEN environment
  variables must be set`, which is a poor first five minutes for a new
  colleague. The file is now read at startup, before anything looks at the
  environment, so every setting the controller takes from there —
  `NETBOX_URL`, `NETBOX_TOKEN`, `IGNORE_SSL_ERRORS`, `DEVICETYPE_LIBRARY`,
  `MODULETYPE_LIBRARY`, `IGNORED_FILES` — can come from it.

  Values already exported win over the file, so a stale `.env` in a working
  directory cannot shadow a CI variable. A missing `.env` is ignored (CI has no
  file); a `--config` path given explicitly must exist, so a typo fails instead
  of running against the wrong NetBox. `.env.example` is the committed
  template, and the "must be set" error now names the file to copy.
- **`.github/workflows/ci.yml`.** The repository lives on GitHub while its
  pipeline lives in `.gitlab-ci.yml`, so a pull request opened here ran no
  checks at all — including `yamlcheck`, the one that catches a bad inventory
  edit. Formatting, `go vet`, licence headers, the unit tests with the same 65%
  coverage gate, `yamlcheck` and a stamped build now run on every push and pull
  request. The end-to-end suite stays where it can reach a NetBox
  (`make e2e`, or the GitLab `e2e` job). A pull request template asks for the
  dry-run plan, which is the only reviewable form of a data change.

- **Cross-object lint checks in `yamlcheck` (`pkg/lint`).** Model validation
  sees one object at a time, so everything that is only wrong *in relation to
  something else* used to reach the apply: a device type slug that matches no
  definition (the device is skipped with a warning), two devices in one rack
  unit (NetBox rejects the second, after the first has been created), an IP or
  a name used twice (the second write silently overwrites the first), a switch
  port claimed by two cables, an interface the device type does not have.
  `yamlcheck` now reports all of them from the repository alone, before a merge
  request is applied — 37 checks, listed with their severities in the README
  section *Checking the YAML before it reaches NetBox*.

  Findings that are wrong regardless of what NetBox contains are errors and
  fail the run; ones that may be legitimate (a value the device type template
  already supplies, a cable declared from both ends, an address outside every
  declared prefix, a peer device this repository does not manage) are warnings
  and fail only under `--strict`. A kind of object the repository declares none
  of is not checked at all, so managing devices but not sites stays a supported
  partial adoption; `--allow-undeclared-refs` covers the mixed case.

  `yamlcheck` also gained proper argument handling: a directory that holds
  `definitions/` or `inventory/` is now treated as a data directory, so
  `yamlcheck path/to/data-dir` runs model validation and linting on it instead
  of only checking its YAML syntax.
- **An end-to-end test for this repository's own data**
  (`tests/e2e/repo-data.sh`, `make e2e-repo`). The existing e2e run proves the
  controller copes with inventories it has never seen; this proves the
  inventory *in this repository* applies, converges and means what its
  conventions say. Beyond the six properties `run.sh` asserts, it checks the
  objects NetBox ends up holding: that a cable declared on one end only wires
  both, that an interface listed without a `type` matched a template port
  instead of creating a second one beside it, that the storage node is
  installed in its chassis bay, that the switch port kept its access VLAN when
  the `link:` moved to the server side, and that the parked `_new-server.yaml`
  was not applied.

- **`rename_from`: correcting an identifying field now renames the object
  instead of duplicating it.** Every object is matched against NetBox by an
  identifying field — a slug, name, model, prefix or VID — so editing that field
  used to mean the old value stopped matching: the object was created a second
  time and the original left behind, still holding every reference that pointed
  at it, with the run reporting success either way. Declaring the previous value
  finds the object where it actually is and renames it through the ordinary
  diff, so `--dry-run` reports it as the update it is, `--prune` does not reap
  it, and the declaration becomes a no-op once applied and can be removed.

  Supported on all 21 declarable object types: sites, racks, device roles,
  platforms, tenants, tenant groups, tags, custom fields, VRFs, VLAN groups,
  VLANs, prefixes, device types, module types, devices, device interfaces,
  cluster types, cluster groups, clusters, virtual machines and VM interfaces.
  Which field `rename_from` refers to is per object type and documented in the
  README; for slug-identified objects, changing only the display name still
  needs no declaration.

  Renaming is restricted to objects carrying the `gitops` tag, because
  `rename_from` asserts something about an object's past rather than matching
  its current identity, and a typo in it could otherwise seize an unrelated
  object. A declaration that matches several objects fails; one that matches
  objects under both the old and new identity renames nothing and warns.
- **Module types are managed as fully as device types.** `ModuleType` gained
  the component templates NetBox supports on it — interfaces, console and
  console server ports, power ports and outlets, front and rear ports, and
  module bays — plus `part_number`, `airflow`, `comments` and
  `weight`/`weight_unit`. Previously a module type was created as a bare name
  and any hardware it declared was silently dropped.
- **Community module type library import** (`--moduletype-library`,
  `MODULETYPE_LIBRARY`, else `definitions/module_type_library/`), reading the
  `module-types/` tree that ships alongside `device-types/` in the same
  library. Component names containing the `{module}` placeholder are passed
  through verbatim, so NetBox can substitute the bay position at installation
  and the same module type in two bays does not collide. Because NetBox
  requires a module type's manufacturer and model to be unique together and
  module types have no slug, two files claiming the same pair are rejected —
  with every conflict reported at once.
- **Component template fields the published library actually uses**: `label`,
  `description`, `color`, `enabled`, `poe_mode`, `poe_type` and `rf_role`, all
  verified writable against a live NetBox. Library-only metadata (elevation
  image flags, module type profiles, `_is_power_source`) is accepted so a real
  file parses, and not sent.
- **Apache License 2.0.** The full licence text is in `LICENSE`, and every
  source file carries an `SPDX-License-Identifier: Apache-2.0` line so licence
  scanners can identify the project without parsing it. `make lint` fails if a
  Go file is missing the header, and the container image carries the standard
  OCI annotations including `org.opencontainers.image.licenses`.
- **Pre-flight validation of NetBox choices and field lengths.** Statuses,
  `face`, `subdevice_role`, `airflow` and `weight_unit` are checked against the
  choice sets NetBox actually publishes, and names, serials, asset tags and
  part numbers against its length limits. These were previously rejected by
  the API partway through a sync, after earlier objects had already been
  written; they now fail in `yamlcheck` and `--dry-run` before anything is
  touched. Lengths are counted in runes, so a name of non-ASCII characters is
  not rejected early. A release that adds a choice can be accommodated by
  extending the sets in `pkg/models/choices.go`.
- **`--version`**, reporting the version, commit and build date stamped at link
  time. `make build` and the CI build job stamp it; an unstamped binary reports
  `dev` rather than claiming a version it does not have.
- **A `Dockerfile`** building a static binary onto a distroless base, with the
  build and runtime images overridable by `--build-arg` for on-prem registries.
- **NetBox v2 API token support.** NetBox 4.5 introduced peppered "v2" tokens,
  sent as `Authorization: Bearer nbt_<key>.<secret>`; the older v1 token is
  sent as `Authorization: Token <key>`. The controller now picks the header
  from the token's own prefix, so either works with no extra configuration.
  This is required for containerised deployments: the official NetBox image's
  bootstrap only ever creates a v2 token, so the controller could not
  authenticate against a default container install at all. NetBox 4.7 removes
  v1 tokens entirely.
- **End-to-end tests against a real NetBox** (`tests/e2e/`, `make e2e`).
  Randomly generated but valid Dell inventories are driven through the full
  lifecycle: model validation, a dry run that must write nothing, an apply, a
  second apply that must have nothing to do, drift detection, and a prune that
  must delete nothing — plus a check that an object the controller does not
  manage survives a prune. Device types are split at random between the native
  format and the community library layout, so both loader paths run on every
  seed. Runs in GitLab CI as the opt-in `e2e` job (`RUN_E2E=true`) with
  NetBox, PostgreSQL and Redis as CI services; `tests/e2e/provision-local.sh`
  covers machines with no container runtime.
- **A `Makefile`** with `build`, `test`, `lint`, `check`, `e2e` and
  `e2e-local` targets.
- **Community device type library support.** Device types published in the
  community library layout (`<library>/<Manufacturer>/<model>.yaml`, one
  document per file, hyphenated component keys) can now be consumed unchanged.
  Point `--devicetype-library` or `DEVICETYPE_LIBRARY` at a library checkout,
  or drop files into `definitions/device_type_library/`. Library entries are
  merged with natively defined device types; a local definition with the same
  slug wins and the override is logged. Library files go through the same
  model validation as native ones, multi-document files are read in full,
  empty files are skipped, and two library files claiming the same slug are
  rejected rather than resolved by directory walk order.
- **Device type component templates** for console ports, console server ports,
  power ports and power outlets, plus the `part_number`, `airflow`,
  `description`, `comments` and `weight`/`weight_unit` fields. Power ports are
  reconciled before power outlets so an outlet can resolve the port that feeds
  it.
- **Multi-position patch panel ports.** Rear ports accept `positions` and
  front ports accept `rear_port_position`, so a breakout panel is no longer
  flattened to a single position per port. Both default to 1, and both are
  applied correctly on NetBox 4.6+ (see the front port fix below). Verified
  against NetBox 4.6.7.
- **NetBox version check.** The controller queries `/api/status/` on startup,
  logs the detected release, and refuses to run against a NetBox older than
  3.6 — the release that renamed the device `device_role` field to `role`.
  A server whose version cannot be determined is reported as a warning and the
  run continues.
- **Ignored files.** Files matching an ignore pattern are skipped while
  loading, so inventory owned by another system can stay in the repository
  without being applied. Defaults to `_*.yaml` and `_*.yml`; override with
  `--ignore-file` or `IGNORED_FILES` (comma-separated globs matched against
  the filename), and load everything with `--include-ignored-files`. Every
  skipped file is logged, and a run that both skips files and uses `--prune`
  warns with the list before deleting anything.

  > **Upgrade note:** the `_*` default is new. If your repository already
  > contains YAML files whose names start with an underscore, they will no
  > longer be applied. Objects this controller previously created from such a
  > file still carry the managed tag and would be deleted as orphans by
  > `--prune`. Check with `--dry-run --prune` before the first pruning run
  > after upgrading, or set `IGNORED_FILES` to a pattern that does not match
  > them.
- **Shared test fixture factories** for the reconciler suite
  (`pkg/reconciler/fixtures_test.go`), so tests state only the fields they are
  actually about.
- This changelog.

### Fixed

- **`cable-conflict` fired on every patch panel.** The check identified a cable
  end by device and port name alone, so a panel's front port "2" and its rear
  port "2" — two separate objects in NetBox, and the whole point of a patch
  panel — looked like one port holding two cables. A correctly modelled fibre
  run (server to front port, rear-to-rear backbone between panels, front port
  to switch on the far side) failed the linter with two errors it could do
  nothing about. A cable end now carries which port collection it belongs to,
  taking the far end from the same role-based rule the reconciler applies:
  patch panel to patch panel is the rear-to-rear backbone, anything else into
  a patch panel lands on its front port. Two cables on one front port are
  still an error, and the message now names the port kind.
- **`address_role: primary` inside an `ip:` mapping never reached NetBox.** The
  role can be written on the interface or beside the address it applies to, and
  the linter has always accepted both — but the sync and the Terraform
  generator only ever read the interface-level spelling. An inventory using the
  nested form passed `yamlcheck` (including its `duplicate-primary-ip` check)
  and then left `primary_ip4`/`primary_ip6` unset on every device and VM, with
  no error to say so. Both spellings now go through one shared helper, so what
  the linter reasons about is what gets applied.
- **A quoted value in `.env` followed by a comment kept its quotes.**
  `NETBOX_TOKEN="nbt_…"   # rotated in May` was parsed as the token *with* the
  double quotes attached, so NetBox answered `403 Invalid token` and the run
  stopped at the first request. The inline comment was being stripped before
  the quotes, leaving nothing to match. Quoted values now end at their closing
  quote and whatever follows is discarded.
- **An explicit `--data-dir` silently fell back to `example/`.** Any directory
  without a `definitions/` subdirectory — a typo, a wrong relative path, a
  clone where the private data had not been checked out — resolved to the
  bundled example dataset with only a warning. The run then applied *the
  example objects* to whatever NetBox the token pointed at, and with `--prune`
  deleted every managed object the example does not declare. A directory named
  explicitly must now contain `definitions/` or the run stops; the fallback
  remains only for the default, where it is the convenience it was meant to be.

- **The end-to-end CI job could have been pointed at the real NetBox and wiped
  it.** GitLab ranks a UI-set CI/CD variable (project, group or instance level) above
  anything in `.gitlab-ci.yml`, job-level `variables:` included — so the
  `NETBOX_URL` and `NETBOX_TOKEN` that `go_apply` needs silently overrode the
  throwaway values the `e2e` job declared for itself. Those scripts run
  `--prune` against an empty data directory between seeds, which deletes every
  `gitops`-tagged object. Enabling `RUN_E2E="true"` on a configured project
  would have aimed that at production. The job now exports its target in the
  job shell, where nothing outranks it, rebuilds the token from the service's
  own bootstrap values, and refuses to run if the URL is not the CI service.
- **`go_lint` could not fail.** Its script ran `go fmt ./...`, which *rewrites*
  the files it visits and exits 0 either way, so unformatted code reported
  success — and `allow_failure: true` meant even `go vet` could not block. It
  runs `make lint` now (`gofmt -l` as a check, `go vet`, SPDX headers) and is
  blocking.
- **An empty `COVERAGE_THRESHOLD` silently disabled the coverage gate.** awk
  compares a non-numeric operand as a string, so `"79.5" < ""` is false and the
  gate passed. The threshold and the measured total are both validated as
  numbers before the comparison, and either one being unreadable fails the job.
- **`debug_environment` declared `dependencies: [go_build]` from the same
  stage**, which GitLab rejects — `dependencies` may only name jobs from an
  earlier stage. It moved to the `validate` stage, where the dependency is
  legal and the binary it prints is actually available.
- **TLS certificate verification was disabled on every connection.**
  `InsecureSkipVerify: true` was hardcoded in the HTTP client, while the README
  offered `IGNORE_SSL_ERRORS` as an opt-in "for dev environments only" that no
  code read. Every run against every instance, production included, accepted
  any certificate presented to it — silently. Certificates are now verified,
  and `IGNORE_SSL_ERRORS` (`true`, `1`, `yes`, `on`) is the real opt-out; a run
  that sets it logs a warning saying so.

  > **Action required** if you sync a NetBox behind a self-signed or internal
  > CA certificate: the run will now fail on the certificate. Either trust the
  > CA on the machine (right) or set `IGNORE_SSL_ERRORS=true` in `.env` (quick).
- **The sample hardware inventory could not be applied to a real NetBox at
  all.** All three leaf switches declared `position: 40` with no `face`, and
  NetBox answers `400 {"face": ["Must specify rack face when defining rack
  position."]}` — so the run stopped at the first switch, and every interface,
  IP and cable behind it was never created. It went unnoticed because the
  end-to-end tests only ever applied *generated* data, which always emits a
  face; `tests/e2e/repo-data.sh` now applies this repository's data too. The
  switches declare `face: "front"`, and `position-without-face` is a lint
  error, so the next occurrence is caught before an apply rather than by it.
- **`tests/e2e/provision-local.sh` printed an API token that did not work.**
  It minted the v1 token by assigning `plaintext=` directly, but
  `Token.save()` treats an instance whose `token` attribute is None as one
  needing a value and generates a random one, whose setter overwrites
  `plaintext`. The instance ended up holding a token nobody knew, and every
  call made with the printed value answered `Invalid v1 token`. The value is
  now passed as `token=` and the script asserts it survived the save.
- **Two device type references in the sample inventory matched no definition,
  and one module type reference in the example data matched none either.** The
  first lint run found them: `berlin-pp-mm-01` asked for `patchpanel-mm-48`
  where the definition's slug is `pp-48-mm-lc`, `berlin-storage-01` asked for
  `isilon-a300` where the definitions declare an `isilon-a300-chassis` and an
  `isilon-a300-node`, and the example GPU server installed `ex-gpu-a100`, a
  part number rather than a slug the module type declared. Each meant the
  object was skipped at apply time with a warning, not an error. The references
  are corrected, the storage system is modelled the way its device types
  describe it (a racked chassis with a node installed into a device bay), and
  the example module types now declare the slugs they are referenced by.
- **A module type reference could silently resolve to the wrong vendor's
  module.** NetBox identifies a module type by manufacturer and model together
  and gives it no slug, so two vendors may ship the same model name — but the
  reference cache is keyed by name alone, and the second one reconciled
  overwrote the first. A device asking for that name got whichever won the
  race. Module types are now also registered under a manufacturer-qualified
  key (`<manufacturer-slug>/<model-slug>`), which `module_type_slug` accepts;
  when a bare name is genuinely ambiguous it is no longer registered at all, so
  the reference reports "not found" instead of installing the wrong hardware,
  and a warning names the qualified keys to use.
- **The device type library import accepted only a fraction of the real
  library.** Strict decoding rejected any file using `label`, `description`,
  `color`, `enabled`, `poe_mode`, `poe_type`, `bridge`, `rf_role`,
  `_is_power_source`, `front_image`, `rear_image` or `is_powered` — which is
  most of them. Verified by parsing the published library: all 5,980 device
  types and 1,949 module types now load. Decoding stays strict, so a
  native-format file dropped into a library root is still caught.
- **Values ending in whitespace no longer produce a phantom update on every
  run.** Django REST Framework strips whitespace from character fields on
  write, so a `comments` value from a YAML block scalar — which ends in a
  newline, as the community library's do — was stored trimmed and never
  matched the payload. String values are now trimmed to match what NetBox
  will store.
- **Pruning no longer deletes an object that is still in use.** An orphan was
  decided purely by "carries the managed tag and was not reconciled this run",
  so removing a site from YAML while a VLAN still declared it made the site
  look orphaned. Deleting it was either refused by NetBox with a 409 — after
  other objects had already been deleted — or, for a nullable foreign key,
  would have silently unlinked the referring object. References made during
  the run are now tracked, and a referenced object is kept with an explanation
  instead of deleted.
- **A failed prune reports what it already deleted.** Pruning stopped at the
  first error, leaving deletions applied and no account of them. It now
  continues past a failure, deletes what it safely can, says plainly when
  NetBox has been left partially pruned, and reports every failure at the end.
- **A runtime failure no longer prints the whole flag list after the error**
  (`SilenceUsage`), which had buried the actual message under 14 lines of
  usage text.
- **An object declaring no `status` no longer fails the run.** NetBox rejects
  an empty status with "This field may not be blank" instead of applying its
  own default, and sites, racks, VLANs and prefixes sent the field
  unconditionally — so omitting `status`, which the schema marks optional,
  aborted the sync. All four now default to `active`, as devices already did.
- **LAG `members` is implemented.** The field existed in the model and the
  README advertised automatic LAG configuration, but no reconciler ever read
  it: a bond was created with no legs and nothing said so. Members are now
  bound to their LAG after all of a device's interfaces exist, so the LAG may
  be declared before or after them, and a member that does not exist is
  reported rather than passed over.
- **Front ports are wired correctly on NetBox 4.6+.** That release replaced a
  front port's singular `rear_port`/`rear_port_position` fields with a
  `rear_ports` list of `{position, rear_port, rear_port_position}` mappings,
  and accepts the old fields silently instead of rejecting them — so front
  ports were created unwired and re-`PATCH`ed on every run. The shape is now
  chosen from the release reported by `/api/status/`, and the mappings are
  compared as an order-insensitive set so they stay idempotent.
- **A device in a bay is no longer given a rack.** NetBox derives a
  bay-mounted device's location from its parent, and installing it into the
  bay clears `rack`/`position`/`face` — so a blade that inherited `rack_slug`
  from its file's `defaults` had the rack set on every run and cleared again
  on every install.
- **`rack_slug` now resolves.** NetBox racks have no slug of their own — they
  are identified by name within a site — so the YAML slug only ever existed
  locally and nothing registered it. Every device was silently created with no
  rack, position or face, on every run. Racks are now registered site-scoped
  under their YAML slug, and a `rack_slug` that still cannot be resolved is
  reported instead of dropped in silence.
- **`--dry-run` works against an empty NetBox.** An object planned but never
  written is registered with id 0, and NetBox rejects `site_id=0` as an
  invalid choice rather than returning no results — so the first dry-run, the
  run the README tells you to start with, aborted. A lookup scoped by a
  reference that does not exist yet cannot match anything, so it is now
  treated as a create instead of being sent to the API.
- **VRF-scoped prefixes are no longer created twice.** The global cache is
  loaded once, before any phase runs, and `ReconcileVRFs` never registered the
  VRFs it created — so on a fresh NetBox every prefix referencing a VRF was
  created in the global table, and the next run created a second, correctly
  scoped copy beside it. The stale copy persisted until a `--prune` run reaped
  it. VRFs are now registered on creation, as sites, roles and device types
  already were. Found running against a live NetBox 4.6.7.
- **Component templates are no longer re-`PATCH`ed on every run.** The four new
  template endpoints (console port, console server port, power port, power
  outlet) were not registered as untaggable, so the managed tag was injected
  into payloads NetBox silently drops. The tag never came back, the field
  always looked changed, and the diff was invisible because `tags` is hidden
  from diff output. Found by running against a real NetBox 4.6.7.
- **A device type declaring `device_bays` without `subdevice_role: parent`** is
  now rejected by model validation. NetBox refuses the bay with a message that
  names the constraint but not the fix, after the device type itself has
  already been written. The bundled example chassis had this defect and could
  never have been applied to a real NetBox.
- **Decimal fields no longer produce a phantom update on every run.** NetBox
  renders decimal fields (`u_height`, `weight`) as JSON strings when its
  serializer coerces decimals, while the payload carries a number, so the two
  never compared equal and every affected device type was re-`PATCH`ed on
  every sync. Values are now compared numerically when one side is a number
  and the other is a string that cleanly parses as one.
- **Parent devices are reconciled before their children.** A device with
  `parent_device` is installed into a bay on that parent, which is resolved
  with a live NetBox lookup. Devices were previously reconciled in the order
  the loader read the files (lexical by filename, then by position within each
  file), so a child that happened to be listed first aborted the run with
  `parent device X not found`. Devices are now topologically sorted, so a
  parent always precedes its children at any nesting depth, and file layout no
  longer matters. A `parent_device` cycle is reported before any change is
  applied.

### Changed

- **One coverage gate, in the Makefile.** `.gitlab-ci.yml` and
  `.github/workflows/ci.yml` each carried their own copy of the shell that
  reads the total and compares it — and they measured different things, GitLab
  over `./pkg/...` and GitHub over `./...`, while sharing one threshold. Both
  now call `make coverage`, so the scope and the bar are defined once; a CI
  variable still overrides the threshold where you want a different one.
- **The Proxmox module's status is stated consistently.** `providers.tf` and
  `versions.tf` said it was "validated against the live cluster" while the
  README said it had never been run against one; the validation they record was
  on the older 0.83.x provider line, and the current `~> 0.109.0` pin has not
  been exercised. `terraform/VALIDATION.md` is the sequence for settling
  that — one throwaway VM through plan, apply, converge and destroy — and names
  the failure modes a first run meets: the NetBox cluster name being passed
  through as a Proxmox pool ID that must already exist and cannot contain
  spaces, unmapped VLAN names, and the guest-agent default that makes an apply
  hang until it times out when the template has no agent.

- **The data directory is loaded in one place.** `yamlcheck` and the controller
  each had their own copy of "load every definitions and inventory folder, merge
  the type libraries"; they are now one `loader.LoadDataset`, so the two cannot
  drift apart on which folders exist or how libraries merge. `lint.Dataset` is
  an alias for `loader.Dataset`, so nothing that used it had to change.

- **The historical planning documents are gone.** `docs/BUGFIX_PLAN.md`,
  `docs/AUDIT_AND_ROADMAP.md`, `docs/PLAN_VIRTUALIZATION.md` and
  `docs/PLAN_YAML_VM_PIPELINE.md` recorded work that is finished, alongside
  status claims that had drifted out of date — a reader looking for how to
  operate the thing found a June 2026 audit and two design records instead.
  What they documented that is still true lives in the README (phase order,
  the object tables) and `terraform/README.md` (the Proxmox pipeline).
  `docs/MISSING_FEATURES.md` is now `docs/ROADMAP.md`, carrying only what is
  actually missing.
- **The README's *Common Errors* section leads with the errors you can still
  hit.** Three of the four are now reported by `yamlcheck` before any request
  is made, and it says so; the rack-face rejection that the end-to-end suite
  found on real hardware data was added.

- **Every CI/CD variable is now documented where it is set.** The header of
  `.gitlab-ci.yml` listed six of them and omitted the two that are actually
  required (`NETBOX_URL`, `NETBOX_TOKEN`), along with `OPENTOFU_IMAGE`,
  `ENABLE_PROXMOX`, `COVERAGE_THRESHOLD` and `TF_ALLOW_DESTROY`. All of them
  are listed there and in `CI_CD.md` with their defaults and exact effect,
  including the two gotchas: the `RUN_*`/`ENABLE_*` comparisons are literal, so
  `"False"` skips nothing and `"True"` enables nothing; and a project-level
  variable outranks anything this file says. The stale "current coverage is
  ~73%" note is now ~79%, measured.
- `tests/e2e/rename.sh` honours `E2E_KEEP`, which `tests/e2e/README.md`
  documents for the suite as a whole and the other two scripts already
  respected.
- **The README opens with a quickstart instead of reference material.** Build,
  configure, dry-run against the example data, edit, check, plan — one screen at
  the top, followed by a table mapping the task you have to the section that
  covers it. Previously the first instruction on how to *run* the thing was at
  line 556, after the device type library semantics, the module type ambiguity
  rules and the rename table.
- **The German comments in `definitions/` are in English**, like the rest of the
  repository, and the `# NEU` markers that had stopped being new are gone.
  The planning documents that still claimed `yamlcheck` skips model validation
  were corrected; they have since been replaced by `docs/CONFIGURATION.md` and
  `docs/ROADMAP.md`.
- **The sample hardware inventory now uses the conventions the README
  documents.** `inventory/hardware/active/` was written in the classic form
  and predated them: it repeated `type` and `enabled: true` on every interface
  (both already supplied by the device type template and the default), repeated
  the site, role, device type and rack on every device, and declared each cable
  from both ends — so adding one server meant editing two files. The files now
  use the grouped `defaults` form, list an interface only for what is specific
  to the device, and declare every cable from the endpoint side only; the
  switches carry the VLANs and management IPs their ports need and no `link:`.
  Nothing about the resulting NetBox state changes. A parked
  `_new-server.yaml` holds a skeleton to copy from, and the Munich lab server
  moved into its own `munich-lab.yaml` to show the one-file-per-site pattern.
- `DeviceType.UHeight` is now a decimal rather than an integer, so half-height
  (0.5U) device types are represented correctly. Existing whole-number
  definitions are unaffected.
- The phase order and the two intra-phase orderings (cables applied in a
  second pass, parents before children) are documented in the README, and the
  forward phase order is now pinned by a test alongside the existing prune
  order test.
