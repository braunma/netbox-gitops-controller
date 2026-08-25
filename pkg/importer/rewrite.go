// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// SandboxTag is the default tag stamped on rewritten objects.
const SandboxTag = "sandbox"

// rewriting reports whether a sandbox rewrite is in effect.
func (rc *runContext) rewriting() bool { return len(rc.opts.Rewrite.Sites) > 0 }

// siteOut maps a source site slug to its rewrite target, honouring an explicit
// mapping first and the "*" wildcard second. Without a rewrite, or for a slug
// no mapping covers, it returns the input unchanged.
func (rc *runContext) siteOut(slug string) string {
	if !rc.rewriting() || slug == "" {
		return slug
	}
	if v, ok := rc.opts.Rewrite.Sites[slug]; ok {
		return v
	}
	if v, ok := rc.opts.Rewrite.Sites["*"]; ok {
		return v
	}
	return slug
}

// nameOut prefixes a name-identified object's name under a rewrite, so a
// rewritten dataset cannot collide with production identities.
func (rc *runContext) nameOut(name string) string {
	if !rc.rewriting() || name == "" || rc.opts.Rewrite.NamePrefix == "" {
		return name
	}
	return rc.opts.Rewrite.NamePrefix + name
}

// rackSlugOut is the slug a device references its rack by, consistent with how
// the rack itself is emitted: the rack's name is prefixed under a rewrite, and
// its slug is derived from that prefixed name.
func (rc *runContext) rackSlugOut(rackName string) string {
	if rackName == "" {
		return ""
	}
	return utils.Slugify(rc.nameOut(rackName))
}

// vrfOut returns the VRF an imported prefix or address should carry. Under a
// rewrite with a scratch VRF, everything is forced into that VRF so rewritten
// IPAM cannot match production; otherwise the object's own VRF is kept.
func (rc *runContext) vrfOut(current string) string {
	if rc.rewriting() && rc.opts.Rewrite.VRF != "" {
		return rc.opts.Rewrite.VRF
	}
	return current
}

// tags returns an object's emitted tag slugs: the stripped NetBox tags, plus
// the sandbox tag when rewriting so the rehearsal's objects are identifiable
// and excludable afterwards.
func (rc *runContext) tags(obj client.Object) []string {
	base := tagSlugs(obj, rc.strip)
	if rc.rewriting() {
		return sortedUnique(append(base, rc.sandboxTag()))
	}
	return base
}

// sandboxTag is the configured sandbox tag, or the default.
func (rc *runContext) sandboxTag() string {
	if rc.opts.Rewrite.Tag != "" {
		return rc.opts.Rewrite.Tag
	}
	return SandboxTag
}
