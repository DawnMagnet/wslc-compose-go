// Package labels defines the container labels wslc-compose uses as its only
// source of state. The keys deliberately mirror Docker Compose
// (com.docker.compose.*) so existing tooling and muscle memory keep working;
// the configuration hash lives under a wslc-specific key.
package labels

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Label keys written on every container, network and volume created by wslc-compose.
const (
	Project     = "com.docker.compose.project"
	Service     = "com.docker.compose.service"
	Number      = "com.docker.compose.container-number"
	OneOff      = "com.docker.compose.oneoff"
	WorkingDir  = "com.docker.compose.project.working_dir"
	ConfigFiles = "com.docker.compose.project.config_files"
	Network     = "com.docker.compose.network"
	Volume      = "com.docker.compose.volume"
	ConfigHash  = "com.wslc.compose.config-hash"
	Version     = "com.wslc.compose.version"
)

// Meta carries the project-wide values stamped on every container.
type Meta struct {
	Project     string
	WorkingDir  string
	ConfigFiles []string
	Version     string
}

// Container returns the full label set for one container of a service.
// A zero number marks a one-off container (`wslc-compose run`).
func (m Meta) Container(service string, number int, hash string) map[string]string {
	l := map[string]string{
		Project:    m.Project,
		Service:    service,
		OneOff:     "False",
		ConfigHash: hash,
	}
	if number > 0 {
		l[Number] = strconv.Itoa(number)
	} else {
		l[OneOff] = "True"
	}
	if m.WorkingDir != "" {
		l[WorkingDir] = m.WorkingDir
	}
	if len(m.ConfigFiles) > 0 {
		l[ConfigFiles] = strings.Join(m.ConfigFiles, ",")
	}
	if m.Version != "" {
		l[Version] = m.Version
	}
	return l
}

// ProjectFilter returns the `--filter` value that selects a project's containers.
func ProjectFilter(project string) string { return "label=" + Project + "=" + project }

// Flags renders labels as repeated `-l key=value` arguments in a stable order.
func Flags(l map[string]string) []string {
	out := make([]string, 0, 2*len(l))
	for _, k := range slices.Sorted(maps.Keys(l)) {
		out = append(out, "-l", k+"="+l[k])
	}
	return out
}

// Merge returns a new map holding all entries of ms; later maps win.
func Merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		maps.Copy(out, m)
	}
	return out
}
