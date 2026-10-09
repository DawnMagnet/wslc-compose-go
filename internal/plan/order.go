package plan

import (
	"fmt"
	"maps"
	"slices"
)

// Order returns service names so that every service comes after its
// dependencies (or before them when reverse is set). Ties are broken
// alphabetically, which makes dry-run output and golden tests deterministic;
// compose-go's graph walker visits independent services in map order.
// Dependencies missing from deps are ignored; cycles are an error.
func Order(deps map[string][]string, reverse bool) ([]string, error) {
	indeg := map[string]int{}
	users := map[string][]string{}
	for svc, ds := range deps {
		indeg[svc] += 0
		for _, d := range ds {
			if _, ok := deps[d]; ok && d != svc {
				indeg[svc]++
				users[d] = append(users[d], svc)
			}
		}
	}
	var out []string
	ready := []string{}
	for _, s := range slices.Sorted(maps.Keys(indeg)) {
		if indeg[s] == 0 {
			ready = append(ready, s)
		}
	}
	for len(ready) > 0 {
		s := ready[0]
		ready = ready[1:]
		out = append(out, s)
		for _, u := range users[s] {
			if indeg[u]--; indeg[u] == 0 {
				ready = append(ready, u)
				slices.Sort(ready)
			}
		}
	}
	if len(out) != len(indeg) {
		return nil, fmt.Errorf("dependency cycle between services")
	}
	if reverse {
		slices.Reverse(out)
	}
	return out, nil
}
