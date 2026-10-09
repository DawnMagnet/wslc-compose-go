package project

import (
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// AnonSuffix marks volume keys synthesized for anonymous volumes.
const AnonSuffix = "_anon"

// NameAnonymousVolumes rewrites anonymous volume mounts (`- /data`), which
// wslc rejects, into project-scoped named volumes declared on the project:
// key `<service>_<path>_anon`, name `<project>_<key>`. Names are stable, so
// data survives container recreation exactly like Compose's anonymous
// volumes do, and `down -v` removes them. Replicas of one service share the
// volume (wslc has no per-container anonymous volumes).
func NameAnonymousVolumes(p *types.Project) {
	rewrite := func(services types.Services) {
		for _, name := range slices.Sorted(maps.Keys(services)) {
			s := services[name]
			changed := false
			for i, v := range s.Volumes {
				if v.Type != types.VolumeTypeVolume || v.Source != "" {
					continue
				}
				key := name + "_" + sanitize(v.Target) + AnonSuffix
				if p.Volumes == nil {
					p.Volumes = types.Volumes{}
				}
				if _, ok := p.Volumes[key]; !ok {
					p.Volumes[key] = types.VolumeConfig{Name: p.Name + "_" + key}
				}
				s.Volumes[i].Source, changed = key, true
			}
			if changed {
				services[name] = s
			}
		}
	}
	rewrite(p.Services)
	rewrite(p.DisabledServices)
}

// sanitize turns a container path into a volume-name fragment: lowercase
// [a-z0-9] runs joined by "_" ("/var/lib/data" -> "var_lib_data").
func sanitize(path string) string {
	f := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	if len(f) == 0 {
		return "root"
	}
	return strings.Join(f, "_")
}
