# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Because this controller can **delete objects from a live NetBox** when run with
`--prune`, treat this file as an upgrade safety document rather than
bookkeeping: read the entries between the version you last ran and the one you
are about to run before applying a sync with pruning enabled.

## [Unreleased]

### Added

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

- `DeviceType.UHeight` is now a decimal rather than an integer, so half-height
  (0.5U) device types are represented correctly. Existing whole-number
  definitions are unaffected.
- The phase order and the two intra-phase orderings (cables applied in a
  second pass, parents before children) are documented in the README, and the
  forward phase order is now pinned by a test alongside the existing prune
  order test.
