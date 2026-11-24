// Package roles resolves which provider profile + model fills each council
// role (proposer, reviewer, judge, red team) for a run. Assignments are
// stored per account in two scopes: a global set and an optional overriding
// set per workspace. Resolution precedence is workspace set -> global set ->
// the legacy default derived from the account's provider profiles.
package roles

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	RoleProposer = "proposer"
	RoleReviewer = "reviewer"
	RoleJudge    = "judge"
	RoleRedTeam  = "red_team"
)

const (
	ScopeGlobal    = "global"
	ScopeWorkspace = "workspace"
)

const (
	SourceWorkspace = "workspace"
	SourceGlobal    = "global"
	SourceLegacy    = "legacy"
)

// Slot is one assignment as stored and as exchanged over the API.
type Slot struct {
	Role              string          `json:"role"`
	Ordinal           int             `json:"ordinal"`
	ProviderProfileID string          `json:"provider_profile_id"`
	Model             string          `json:"model"`
	Params            json.RawMessage `json:"params,omitempty"`
	Enabled           bool            `json:"enabled"`
}

// ProfileInfo is the resolver/validator view of a provider profile. It never
// carries the API key itself, only whether one is configured.
type ProfileInfo struct {
	ID       string
	Provider string
	Model    string
	HasKey   bool
}

// Issue is one validation finding. Errors block a save; warnings do not.
type Issue struct {
	Code    string `json:"code"`
	Role    string `json:"role,omitempty"`
	Ordinal *int   `json:"ordinal,omitempty"`
	Message string `json:"message"`
}

func issue(code, role string, ordinal int, format string, args ...any) Issue {
	return Issue{Code: code, Role: role, Ordinal: &ordinal, Message: fmt.Sprintf(format, args...)}
}

// MaxOrdinal bounds the slots per role: proposers/reviewers get a full
// bench, judge and red team get a primary plus up to two failovers.
func MaxOrdinal(role string) int {
	switch role {
	case RoleProposer, RoleReviewer:
		return 7
	case RoleJudge, RoleRedTeam:
		return 2
	}
	return -1
}

func ValidRole(role string) bool { return MaxOrdinal(role) >= 0 }

// ParseParams validates the slot parameter object. The supported keys mirror
// what the runtime can actually apply: temperature is forwarded to the
// provider request; max_output_tokens is stored for forward compatibility.
func ParseParams(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("params must be a JSON object")
	}
	for key, value := range params {
		switch key {
		case "temperature":
			number, ok := value.(float64)
			if !ok || number < 0 || number > 2 {
				return nil, fmt.Errorf("temperature must be a number between 0 and 2")
			}
		case "max_output_tokens":
			number, ok := value.(float64)
			if !ok || number != float64(int64(number)) || number < 1 || number > 128000 {
				return nil, fmt.Errorf("max_output_tokens must be an integer between 1 and 128000")
			}
		default:
			return nil, fmt.Errorf("unsupported param %q", key)
		}
	}
	return params, nil
}

// Temperature extracts the configured temperature, if any.
func Temperature(raw json.RawMessage) *float64 {
	params, err := ParseParams(raw)
	if err != nil {
		return nil
	}
	if value, ok := params["temperature"].(float64); ok {
		return &value
	}
	return nil
}

func slotKey(role string, ordinal int) string { return role + "/" + fmt.Sprint(ordinal) }

func describeSlot(slot Slot) string {
	return strings.TrimSpace(fmt.Sprintf("%s #%d", slot.Role, slot.Ordinal))
}
