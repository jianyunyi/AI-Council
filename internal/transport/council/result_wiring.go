package council

import (
	"github.com/aicouncil/aicouncil/internal/app/group"
)

// InstallGroupResults wires Part B P9 into the group service: the
// result copy-up source over the landed Part A artifact store (W8).
// Call after InstallGroupPushdown. When it is never called (or the
// API has no artifact store), the group service keeps the exact P8
// behavior: requirements end at executed/execution_failed and the
// result endpoints answer 503 results_unavailable.
func InstallGroupResults(a *API, svc *group.Service) {
	if a == nil || svc == nil || a.db == nil || a.artifactRepo == nil {
		return
	}
	svc.EnableResults(artifactBridge{api: a})
}
