package council

import (
	"github.com/aicouncil/aicouncil/internal/app/group"
	"github.com/aicouncil/aicouncil/internal/council/roles"
)

// InstallGroupPushdown wires Part B P8 into the group service: the
// red-team seat source (Part A resolver), the council task bridge and
// the model-workspace lookup. Call after InstallGroups, once a roles
// resolver exists (which needs the provider key service). When it is
// never called, the group service keeps the exact P7 behavior:
// review_passed is the end of the pipeline and the approve endpoint
// answers 503 pushdown_unavailable.
func InstallGroupPushdown(a *API, svc *group.Service, resolver *roles.Resolver) {
	if a == nil || svc == nil || resolver == nil || a.db == nil || a.aiModels == nil {
		return
	}
	svc.EnablePushdown(
		redTeamSeatAdapter{api: a, resolver: resolver},
		newTaskBridge(a),
		modelWSLookup{api: a},
	)
}
