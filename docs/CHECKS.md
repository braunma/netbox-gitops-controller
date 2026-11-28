# Checking the YAML

What catches a mistake before it reaches NetBox: editor schemas, the offline checks, the merge request report, and validation against a live NetBox.

## Editing with a schema

Every kind of data file has a JSON Schema in `schema/`, generated from the Go
models the loader decodes into (`go run ./cmd/schemagen`), so an editor knows the fields,
the short names, the allowed values of `status`, `face` and the like, and
every shape a file may take (list, grouped `defaults:` + `items:`, single
object, `interfaces:` as a mapping, the `ip:` and `via:` shorthands). The
first line of each data file points at it:

```yaml
# yaml-language-server: $schema=../../../schema/device.json
```

VS Code with the YAML extension (and any editor with a YAML language server)
then completes field names, underlines an unknown key and offers the choice
values while typing — the same judgement the loader makes, before the file is
saved. `go run ./cmd/schemagen --headers <data-dir>`, run from the repository root, adds or refreshes the
line in every file of a data directory; `make lint` fails when `schema/` is
behind the models.

## Merge request feedback

Problems and findings reach the merge request instead of staying in a job
log: `yaml_check` writes a GitLab Code Quality report
(`yamlcheck --codequality`), the merge request widget lists every load
problem and lint finding, and the diff view pins each to the file and line of
the object it is about where the loader knows it. A YAML syntax error stops the
job before the report is written and stays in the job log. The dry-run plan stays where it was, in the `go_plan`
job log and its `plan-output.txt` artifact.

## Checking the YAML before it reaches NetBox

`yamlcheck` reads the data directory and reports what is wrong with it without
opening a connection — every problem of a stage at once, one line each. The
stages gate each other: YAML syntax, then the typed model validation (unknown or
misspelled keys, required fields, cross-field constraints, NetBox choice
values), then cross-object
checks that no single object can see on its own.

```bash
go run ./cmd/yamlcheck                       # definitions/, inventory/ and example/
go run ./cmd/yamlcheck path/to/data-dir      # a directory holding definitions/ and inventory/
go run ./cmd/yamlcheck --strict              # fail on warnings too
```

**Errors** — wrong regardless of what NetBox contains, so they fail the run:

| Check | What it catches |
|---|---|
| `duplicate-device`, `duplicate-vm`, `duplicate-interface` | the same name declared twice, where the second declaration silently overwrites the first |
| `duplicate-ip` | one address on two interfaces in the same VRF (the global table counts as one) |
| `duplicate-primary-ip` | two interfaces claiming the primary IP for one address family, where NetBox keeps only the one written last |
| `rack-collision` | two devices occupying the same rack unit, computed from each device type's `u_height` |
| `cable-conflict` | a port claimed by two different cables (a patch panel's front port and rear port of the same name are two ports, resolved by role as the reconciler does) |
| `unknown-peer-port` | a cable to a port its peer's device type does not have |
| `untyped-interface` | an interface that is neither an interface template on the device type nor carries a `type`, so NetBox has nothing to create it from |
| `untyped-port`, `unknown-rear-port` | the same for a front or rear port, and a front port mapped to a rear port that neither the device type nor the device declares |
| `unknown-lag-member` | a LAG whose `members` name an interface the device does not have |
| `unknown-module-bay`, `unknown-device-bay`, `missing-device-bay` | a module or child device installed into a bay the parent's device type does not have, or a child device that names a parent but no `device_bay` |
| `unknown-site`, `unknown-role`, `unknown-rack`, `unknown-device-type`, `unknown-vlan`, `unknown-vrf`, `unknown-module-type`, `unknown-cluster`, `unknown-platform`, `unknown-tenant`, `unknown-parent-device` | a reference that resolves to nothing declared here (VLANs are matched within the device's site, as NetBox resolves them) |
| `unknown-tag` | a `tags:` entry that names no tag in `definitions/extras` (the managed `gitops` tag is always accepted; silent when the repository defines no tags at all) |
| `unknown-custom-field`, `custom-field-wrong-type` | a device setting a custom field this repository does not declare, or declares for another content type — the mistake a repository makes when it ingests `hw_*` facts without copying the definitions in |
| `invalid-ip`, `network-address`, `broadcast-address` | an address that is not a host address (a /31 point-to-point link is not flagged) |
| `ip-vrf-mismatch` | an address whose VRF differs from the VRF of the most specific declared prefix it falls into (an address with no VRF gets its prefix's VRF, see [What the controller fills in](WRITING_YAML.md#what-the-controller-fills-in)) |

**Warnings** — legitimate in some repositories, so they only fail under
`--strict`: `redundant-interface-type`, `redundant-port-type`, `redundant-port`
(a port entry that restates nothing but its template) and `redundant-enabled`
(a value the device type template or the default already supplies), `interface-type-override`, `port-type-override`,
`interface-not-in-template`, `cable-defaults-off-panel` (a `cable_defaults`
block on a device that is not a patch panel, where it does nothing),
`cable-declared-twice` (the same cable from both ends),
`undeclared-peer-device`, `ip-outside-prefix`, `unracked-device`,
`module-type-without-slug`.

Two things worth knowing about the reference checks:

  * A kind of object the repository declares **none** of is not checked at all.
    A repository that manages devices but not sites is a legitimate partial
    adoption, not one full of broken site references.
  * If you do declare everything but reference an object created outside this
    repository, `--allow-undeclared-refs` reports those as warnings instead.

## Checking against the live NetBox

`yamlcheck` cannot know what *your* instance accepts. The choices a NetBox
offers depend on its release and its plugins — a 4.6 offers well over 200 interface types —
so a hardcoded list can only approximate them, and a value it gets wrong
becomes a `400` partway through an apply, after earlier objects have been
written.

`validate` asks the instance instead:

```bash
./netbox-gitops validate               # against $NETBOX_URL, writes nothing
./netbox-gitops validate --skip-references
```

It reads each endpoint's `OPTIONS` response — the authority on what that server
accepts — and checks the choice values and string lengths the YAML sets (statuses,
interface, port and cable types, modes, faces, airflow, names, serials) against
it. References the repository does not declare itself are looked up, so a site
that exists only in NetBox is accepted and one that exists nowhere is reported.
Everything wrong is reported at once, rather than stopping at the first
rejection:

```text
✗ device srv-01 interface eth0: type "25gbase-x-sfp29" is not a value this
  NetBox accepts; did you mean "25gbase-x-sfp28"? (invalid-choice)
✗ cable declared on device srv-01 interface eth0: type "dac-activ" is not a
  value this NetBox accepts; did you mean "dac-active"? (invalid-choice)
✗ site does-not-exist: is declared nowhere in this repository and does not
  exist in NetBox either (missing-reference)
```

Every request it makes is a read, and the client runs in dry-run mode
throughout, so it cannot write even if asked to.

The three checks answer different questions, and a merge request is worth
running all three through:

| | Needs a NetBox | Answers |
|---|---|---|
| `yamlcheck` | no | Is this repository coherent with itself? |
| `validate` | yes | Will this instance accept these values? |
| `--dry-run` | yes | What would change? |
