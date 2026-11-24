package roles

import (
	"fmt"
)

// ValidateSet checks a candidate full set against the account's profiles.
// It returns blocking errors and non-blocking warnings separately. The codes
// are part of the API contract: profile_not_found, profile_key_missing,
// model_not_offered, duplicate_slot, params_out_of_range, cardinality.
// (profile_disabled and model_unverified do not occur in this codebase:
// profiles have no disabled flag, and a profile's model list is local — the
// single configured model — so it is always verifiable.)
func ValidateSet(profiles []ProfileInfo, slots []Slot) (errs []Issue, warnings []Issue) {
	byID := make(map[string]ProfileInfo, len(profiles))
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	seenSlots := map[string]bool{}
	seenAssignments := map[string]bool{}
	enabled := map[string][]Slot{}
	for _, slot := range slots {
		if !ValidRole(slot.Role) {
			errs = append(errs, Issue{Code: "invalid_role", Role: slot.Role, Message: fmt.Sprintf("unknown role %q", slot.Role)})
			continue
		}
		if slot.Ordinal < 0 || slot.Ordinal > MaxOrdinal(slot.Role) {
			errs = append(errs, issue("ordinal_out_of_range", slot.Role, slot.Ordinal, "%s: ordinal must be between 0 and %d", describeSlot(slot), MaxOrdinal(slot.Role)))
		}
		key := slotKey(slot.Role, slot.Ordinal)
		if seenSlots[key] {
			errs = append(errs, issue("duplicate_slot", slot.Role, slot.Ordinal, "%s: duplicate slot", describeSlot(slot)))
		}
		seenSlots[key] = true
		dupKey := slot.Role + "/" + slot.ProviderProfileID + "/" + slot.Model
		if seenAssignments[dupKey] {
			errs = append(errs, issue("duplicate_slot", slot.Role, slot.Ordinal, "%s: same profile and model assigned twice to the role", describeSlot(slot)))
		}
		seenAssignments[dupKey] = true

		profile, ok := byID[slot.ProviderProfileID]
		if !ok {
			errs = append(errs, issue("profile_not_found", slot.Role, slot.Ordinal, "%s: provider profile %q does not exist for this account", describeSlot(slot), slot.ProviderProfileID))
		} else {
			if !profile.HasKey {
				errs = append(errs, issue("profile_key_missing", slot.Role, slot.Ordinal, "%s: provider profile has no API key configured", describeSlot(slot)))
			}
			if slot.Model != profile.Model {
				errs = append(errs, issue("model_not_offered", slot.Role, slot.Ordinal, "%s: profile offers model %q, not %q", describeSlot(slot), profile.Model, slot.Model))
			}
		}
		if _, err := ParseParams(slot.Params); err != nil {
			errs = append(errs, issue("params_out_of_range", slot.Role, slot.Ordinal, "%s: %v", describeSlot(slot), err))
		}
		if slot.Enabled {
			enabled[slot.Role] = append(enabled[slot.Role], slot)
		}
	}

	// Cardinality is evaluated over enabled slots: disabled slots are kept
	// in the set but never run.
	if count := len(enabled[RoleProposer]); count < 1 || count > 8 {
		errs = append(errs, Issue{Code: "cardinality", Role: RoleProposer, Message: fmt.Sprintf("proposer needs 1..8 enabled slots, has %d", count)})
	}
	if count := len(enabled[RoleReviewer]); count > 8 {
		errs = append(errs, Issue{Code: "cardinality", Role: RoleReviewer, Message: fmt.Sprintf("reviewer allows 0..8 enabled slots, has %d", count)})
	}
	errs = append(errs, checkPrimaryFailover(RoleJudge, enabled[RoleJudge], true)...)
	errs = append(errs, checkPrimaryFailover(RoleRedTeam, enabled[RoleRedTeam], false)...)
	return errs, warnings
}

func checkPrimaryFailover(role string, slots []Slot, required bool) []Issue {
	var errs []Issue
	primaries := 0
	failovers := 0
	for _, slot := range slots {
		if slot.Ordinal == 0 {
			primaries++
		} else {
			failovers++
		}
	}
	if required && primaries != 1 {
		errs = append(errs, Issue{Code: "cardinality", Role: role, Message: fmt.Sprintf("%s needs exactly 1 enabled primary (ordinal 0), has %d", role, primaries)})
	}
	if !required && primaries > 1 {
		errs = append(errs, Issue{Code: "cardinality", Role: role, Message: fmt.Sprintf("%s allows at most 1 enabled primary, has %d", role, primaries)})
	}
	if failovers > 2 {
		errs = append(errs, Issue{Code: "cardinality", Role: role, Message: fmt.Sprintf("%s allows at most 2 failovers, has %d", role, failovers)})
	}
	if failovers > 0 && primaries == 0 {
		errs = append(errs, Issue{Code: "cardinality", Role: role, Message: fmt.Sprintf("%s failovers need an enabled primary", role)})
	}
	return errs
}
