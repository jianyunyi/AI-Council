package verification

import (
	"errors"
	"sort"
)

var ErrUnexpectedChange = errors.New("unexpected workspace change")

func VerifyChangedPaths(approved, actual []string) error {
	allowed := map[string]struct{}{}
	for _, p := range approved {
		allowed[p] = struct{}{}
	}
	for _, p := range actual {
		if _, ok := allowed[p]; !ok {
			return ErrUnexpectedChange
		}
	}
	return nil
}
func MissingApprovedPaths(approved, actual []string) []string {
	have := map[string]struct{}{}
	for _, p := range actual {
		have[p] = struct{}{}
	}
	var missing []string
	for _, p := range approved {
		if _, ok := have[p]; !ok {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	return missing
}
