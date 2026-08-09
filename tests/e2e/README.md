# End-to-end tests

These drive the built binary against a **real NetBox** and assert the
properties that unit tests with an in-memory fake cannot see.

That distinction is not theoretical. Every defect these tests were written for
was invisible to the unit suite, because a fake echoes back whatever you send
it and therefore always agrees with you. A real NetBox does not: it drops
fields it does not recognise, rejects blank values the schema calls optional,
renames fields between releases while still accepting the old ones, and
returns objects in shapes the payload never used.

## What is asserted

For each generated dataset:

1. **Model validation** — `yamlcheck` accepts the data.
2. **Dry-run plans without writing** — `--dry-run` against an empty NetBox
   succeeds and creates no objects.
3. **Apply succeeds.**
4. **A second apply has nothing to do** — `0 created, 0 updated, 0 deleted`.
   This is the invariant that matters most and the one that kept breaking.
5. **Drift detection agrees** — `--dry-run --detailed-exitcode` exits `0` on a
   converged instance.
6. **Pruning a converged instance deletes nothing.**

Then, once: an object the controller does not manage (no `gitops` tag)
**survives** a prune that removes everything else.

## The data

`gen.py` generates random but valid Dell inventories from a seed — PowerEdge
R640/R650/R660/R740/R750/R7615/XE9680, PowerSwitch S4148F/S5248F/S5232F/
N3248TE, an MX7000 chassis with MX750c sleds, and a patch panel with cabling.
It varies counts, names, rack placement, declaration order and which optional
fields are present.

Device types are split at random between the two supported formats — the
native list form under `definitions/device_types/`, and the community device
type library layout under `definitions/device_type_library/Dell/` — so both
loader paths and their merge run on every seed.

Seeds are deterministic: a failure reproduces with the same `E2E_SEEDS`.

## Running them

### In CI (GitLab)

The `e2e` job runs NetBox, PostgreSQL and Redis as CI services. It is opt-in:

```
RUN_E2E=true
```

Set it per-pipeline or as a project variable. The job needs a **Docker
executor** and the three images in `NETBOX_IMAGE`, `POSTGRES_IMAGE` and
`REDIS_IMAGE` — override those to point at your own registry mirror when
running on-prem.

`FF_NETWORK_PER_BUILD: "true"` is set on the job and is required: without it
the service containers share no network and NetBox cannot reach its database.

### Against any disposable NetBox

The harness only needs a URL and a token, so it also runs against a staging
instance:

```bash
export NETBOX_URL=https://netbox-staging.internal
export NETBOX_TOKEN=...            # v1 or v2 (nbt_<key>.<secret>)
make e2e
```

**Never point this at production.** It creates objects and prunes everything
carrying the `gitops` tag between seeds.

### On a machine with no container runtime

`provision-local.sh` installs PostgreSQL, Redis and NetBox from source, then
prints the URL and token to export. Useful on a workstation or a
shell-executor runner:

```bash
make e2e-local
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `NETBOX_URL` | — | Base URL of the target NetBox (required) |
| `NETBOX_TOKEN` | — | API token, v1 or v2 (required) |
| `E2E_SEEDS` | `1 2 3` | Space-separated generator seeds |
| `E2E_KEEP` | `0` | Set to `1` to keep the generated data and logs |
| `E2E_WORK` | temp dir | Where datasets, logs and the binary are written |

## API tokens

NetBox 4.5 introduced "v2" tokens, sent as `Authorization: Bearer
nbt_<key>.<secret>`; older "v1" tokens are sent as `Authorization: Token
<key>`. The controller picks the header from the token's own prefix, so either
works with no extra configuration.

This matters for the CI job: the official NetBox container's bootstrap only
ever creates a v2 token, and only when `API_TOKEN_PEPPER_1`,
`SUPERUSER_API_KEY` and `SUPERUSER_API_TOKEN` are all set — which is why the
job sets all three and assembles `NETBOX_TOKEN` from the key and secret.
NetBox 4.7 removes v1 tokens entirely.
