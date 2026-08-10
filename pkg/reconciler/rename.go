// SPDX-License-Identifier: Apache-2.0

package reconciler

// nonEmpty adapts a declared rename_from value for RenamedLookup, which takes
// the previous identity as an interface so it can carry a VID as well as a
// name. An empty string means "not declared" and must stay nil, or every
// object would look like it had been renamed from "".
func nonEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
