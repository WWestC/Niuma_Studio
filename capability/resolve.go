// Assembly resolution: the two-level selection and the key→asset
// lookups that feed Compose. Both degrade, never error — an assembly
// must not brick a member.
package capability

// EffectiveSkills picks the seat's effective skill keys (Q18 explicit
// takeover): a non-empty staffing list wholly replaces the profile
// default; an empty one inherits it. EffectiveMCPs follows the same
// rule for the server list.
func EffectiveSkills(profile, staffing []string) []string {
	if len(staffing) > 0 {
		return staffing
	}
	return profile
}

// EffectiveMCPs is EffectiveSkills for the MCP server list.
func EffectiveMCPs(profile, staffing []string) []string {
	if len(staffing) > 0 {
		return staffing
	}
	return profile
}

// ResolveSkills turns skill keys into skills through the store, in
// order. A nil store or a missing key contributes nothing (skills
// get removed from the library while assemblies still name them);
// duplicate keys are followed as given.
func ResolveSkills(store *Store, keys []string) []Skill {
	if store == nil || len(keys) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(keys))
	for _, k := range keys {
		if sk, ok := store.GetSkill(k); ok {
			out = append(out, sk)
		}
	}
	return out
}

// ResolveMCPs is ResolveSkills for the MCP server list.
func ResolveMCPs(store *Store, keys []string) []MCPServer {
	if store == nil || len(keys) == 0 {
		return nil
	}
	out := make([]MCPServer, 0, len(keys))
	for _, k := range keys {
		if m, ok := store.GetMCP(k); ok {
			out = append(out, m)
		}
	}
	return out
}
