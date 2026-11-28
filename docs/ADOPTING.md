# Adopting what NetBox already holds

A NetBox that was filled in by hand before this repository existed does not
have to be retyped. `export` reads it and writes every object the repository
does not declare yet as YAML:

```bash
./netbox-gitops export --dry-run          # list the files it would write
./netbox-gitops export                    # write them into definitions/ and inventory/
./netbox-gitops export --site berlin-dc   # one site at a time (shared definitions always)
./netbox-gitops export --out /tmp/review  # somewhere else, to look at first
./netbox-gitops export --all --out /tmp/reference  # everything, declared or not, to compare against
```

`--all` is for a repository that already adopted its objects: it writes them
again, with every field the YAML can carry today, so you can copy over what an
earlier export did not have yet (a `tenant:`, say). It is a reference, not
something to merge. In GitLab, run `netbox_export` with `EXPORT_REFERENCE=true`
and take `export-reference/` from the job artifacts.

**With the GitLab pipeline**, run the manual `netbox_export` job on the
default branch instead: it exports, pushes `chore/netbox-export` and opens the
merge request, so the token never leaves CI. See
[`CI_CD.md`](../CI_CD.md#adopting-existing-objects). Exporting locally, mind that
`.gitignore` lists `/definitions/` and `/inventory/*`: add the new files with
`git add -f definitions/ inventory/`, or most of them are left out of the
commit without a word.

Each object is written under the name or slug it already has, so the next sync
**adopts** it rather than creating a second one: it finds the object, adds the
`gitops` tag, and manages it from then on. Every field the sync would send
carries the value NetBox already holds, so for an object exported unchanged
the first sync changes nothing but the tag list. That includes the object's
existing tags: a sync sets an object's tags to exactly what the YAML declares,
so they are exported, and every tag is exported as a definition.

Then:

1. Read the end of the export's output. It lists everything NetBox holds that
   the YAML cannot carry, and what was therefore left out (see below).
2. `make check` and `./netbox-gitops --dry-run`. The plan should be tag
   additions; anything else is worth a look before merging.
3. Open the merge request. After it is applied the objects are managed like
   any other, **including deletion by `--prune`** once removed from the YAML.

Running `export` again only writes what is still undeclared. It never
overwrites a file: where one exists, the new export is written next to it
(`imported-2.yaml`), so a second site's shared definitions land beside the
first run's. `--force` overwrites and is only accepted with `--out` pointing
outside the data directory, because replacing a file there would undeclare
its objects. Objects that share the identity a sync matches on (two devices of
one name at a site, a VID twice at a site, a VRF name twice) are reported and
left out. Devices go to one
`imported-<site>.yaml` per site, device and module types to one file each under
an `imported/` directory. Nothing is written to NetBox.

**What is not exported, or exported with a caveat.** Each case is reported,
except the first row:

| NetBox holds | What happens |
|---|---|
| A site's region; a VLAN's or prefix's role | Not exported, and not reported. The exported YAML does not set them, so they stay as they are. |
| A device's platform, description, comments, location, OOB IP | Not in the YAML. A sync does not send them, so they stay as they are. |
| Several IP addresses on one interface | The primary one (else the first) is declared; the others stay in NetBox, unmanaged. |
| An address in the global table inside a prefix of a VRF | Not declared: the controller would derive the prefix's VRF, miss the address, and create a copy. |
| A VLAN in no group at a site with exactly one VLAN group | Exported; the first sync moves it into that group (the controller derives it). |
| A prefix with no site whose VLAN is at exactly one site | Exported; the first sync scopes it to that site (derived the same way). |
| VLANs without a site, devices without a name, VM names used twice | Not exported: the repository identifies them by site, name, and name. |
| Cables to circuits, power or console ports; cables the controller would look for differently (e.g. patch panel front to front) | Not exported. |
| Tags on a device role | Cannot be declared; the first sync replaces them with `gitops`. |
| Interfaces, front and rear ports a device type's template already provides | Listed only when they carry something of their own (an IP, VLANs, a cable, a description, tags, ...). |

The plan of that merge request lists one update per adopted object, adding the
`gitops` tag; a cable's adoption is listed too. An interface that is a LAG
member can appear twice, once for itself and once for the LAG binding.
