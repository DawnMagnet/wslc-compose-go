// Package translate maps normalized Compose service definitions to wslc
// command lines. Every function here is pure: given the same project and
// options it always produces the same argv, which keeps golden tests exact.
package translate

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/DawnMagnet/wslc-compose-go/internal/labels"
	"github.com/DawnMagnet/wslc-compose-go/internal/paths"
)

// DefaultDNS is used when a service declares no `dns:`. The wslc utility VM
// forwards DNS to the Windows host resolver, which on many LAN/corporate
// setups answers SERVFAIL for public names from inside containers; pinning a
// public resolver makes image-internal lookups (apt, npm, pip) reliable.
var DefaultDNS = []string{"1.1.1.1"}

// Translator turns services of one project into wslc argv slices.
type Translator struct {
	Project    *types.Project
	Paths      paths.Mapper // host path mapper; nil means identity
	DefaultDNS []string     // applied when a service sets no dns; nil disables
	Meta       labels.Meta  // project-wide labels
}

// RunOptions customizes a single `wslc run` invocation.
type RunOptions struct {
	Name     string   // container name; empty derives it from project/service/number
	Number   int      // replica number (1-based); 0 marks a one-off container
	Hash     string   // config hash label value
	Detach   bool     // add -d
	Remove   bool     // add --rm
	Command  []string // overrides the service command when non-nil
	NoTTY    bool     // drop -t/-i even if the service asks for them
	TTY      bool     // force -i -t (interactive one-off runs)
	Publish  bool     // publish service ports (false for one-off runs unless --service-ports)
	ExtraEnv []string // additional KEY=VALUE pairs (run -e)
}

// ImageName is the image a service runs: `image:` or `<project>-<service>`.
func ImageName(p *types.Project, s types.ServiceConfig) string {
	if s.Image != "" {
		return s.Image
	}
	return p.Name + "-" + s.Name
}

// ContainerName follows Docker Compose v2 naming: container_name or
// `<project>-<service>-<n>`.
func ContainerName(p *types.Project, s types.ServiceConfig, n int) string {
	if s.ContainerName != "" {
		return s.ContainerName
	}
	return fmt.Sprintf("%s-%s-%d", p.Name, s.Name, n)
}

func (t *Translator) path(p string) string {
	if t.Paths == nil {
		return p
	}
	return t.Paths(p)
}

// argv is a tiny builder that skips empty values.
type argv []string

func (a *argv) add(v ...string) { *a = append(*a, v...) }

func (a *argv) opt(flag, v string) {
	if v != "" {
		a.add(flag, v)
	}
}

func (a *argv) each(flag string, vs []string) {
	for _, v := range vs {
		a.add(flag, v)
	}
}

// Run returns the argv (without the binary) for `wslc run`.
func (t *Translator) Run(s types.ServiceConfig, o RunOptions) []string {
	a := argv{"run"}
	if o.Detach {
		a.add("-d")
	}
	if o.Remove {
		a.add("--rm")
	}
	name := o.Name
	if name == "" {
		name = ContainerName(t.Project, s, o.Number)
	}
	a.add("--name", name)
	a.add(labels.Flags(labels.Merge(s.Labels, t.Meta.Container(s.Name, o.Number, o.Hash)))...)
	a.each("-e", append(Env(s.Environment), o.ExtraEnv...))
	if o.Publish {
		a.each("-p", Ports(s.Ports))
	}
	a.add(t.volumes(s)...)
	a.add(t.network(s, o.Number > 0)...)
	a.opt("-h", s.Hostname)
	a.opt("--domainname", s.DomainName)
	dns := []string(s.DNS)
	if len(dns) == 0 {
		dns = t.DefaultDNS
	}
	a.each("--dns", dns)
	a.each("--dns-search", s.DNSSearch)
	a.each("--dns-option", s.DNSOpts)
	a.opt("-u", s.User)
	a.opt("-w", s.WorkingDir)
	a.add(Resources(s)...)
	a.add(Health(s.HealthCheck)...)
	if !o.NoTTY {
		if s.StdinOpen || o.TTY {
			a.add("-i")
		}
		if s.Tty || o.TTY {
			a.add("-t")
		}
	}
	if len(s.Entrypoint) > 0 {
		a.add("--entrypoint", s.Entrypoint[0])
	}
	a.add(ImageName(t.Project, s))
	if len(s.Entrypoint) > 1 {
		a.add(s.Entrypoint[1:]...)
	}
	if o.Command != nil {
		a.add(o.Command...)
	} else {
		a.add(s.Command...)
	}
	return a
}

// Build returns the argv for `wslc build`, or nil when the service has no
// build section. Relative Dockerfiles are resolved against the context
// because wslc resolves -f against its own working directory.
func (t *Translator) Build(s types.ServiceConfig, noCache, pull bool) []string {
	b := s.Build
	if b == nil {
		return nil
	}
	a := argv{"build", "-t", ImageName(t.Project, s)}
	a.each("-t", b.Tags)
	if b.Dockerfile != "" {
		df := b.Dockerfile
		if !filepath.IsAbs(df) && !paths.IsWindows(df) {
			df = filepath.Join(b.Context, df)
		}
		a.add("-f", t.path(df))
	}
	a.each("--build-arg", Env(b.Args))
	a.opt("--target", b.Target)
	a.add(labels.Flags(b.Labels)...)
	if b.Pull || pull {
		a.add("--pull")
	}
	if b.NoCache || noCache {
		a.add("--no-cache")
	}
	a.add(t.path(b.Context))
	return a
}

// Env renders a MappingWithEquals as sorted KEY=VALUE pairs. Variables
// without a value (unresolved pass-through) are skipped because the wslc
// process environment lives on the Windows side.
func Env(m types.MappingWithEquals) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if v := m[k]; v != nil {
			out = append(out, k+"="+*v)
		}
	}
	return out
}

// Ports renders port mappings as `[ip:][published:]target[/proto]`.
func Ports(ps []types.ServicePortConfig) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		s := strconv.FormatUint(uint64(p.Target), 10)
		if p.Published != "" {
			s = p.Published + ":" + s
			if p.HostIP != "" {
				s = p.HostIP + ":" + s
			}
		}
		if p.Protocol != "" && p.Protocol != "tcp" {
			s += "/" + p.Protocol
		}
		out = append(out, s)
	}
	return out
}

func (t *Translator) volumes(s types.ServiceConfig) []string {
	var a argv
	for _, v := range s.Volumes {
		ro := ""
		if v.ReadOnly {
			ro = ":ro"
		}
		switch v.Type {
		case types.VolumeTypeBind:
			a.add("-v", t.path(v.Source)+":"+v.Target+ro)
		case types.VolumeTypeVolume:
			if v.Source == "" { // anonymous volume
				a.add("-v", v.Target+ro)
				continue
			}
			name := v.Source
			if vc, ok := t.Project.Volumes[v.Source]; ok && vc.Name != "" {
				name = vc.Name
			}
			a.add("-v", name+":"+v.Target+ro)
		case types.VolumeTypeTmpfs:
			spec := v.Target
			if v.Tmpfs != nil && v.Tmpfs.Size > 0 {
				spec += ":size=" + strconv.FormatInt(int64(v.Tmpfs.Size), 10)
			}
			a.add("--tmpfs", spec)
		}
	}
	a.each("--tmpfs", s.Tmpfs)
	return a
}

// Network returns the primary project network key of a service, or "" if
// the service uses network_mode or has no networks.
func Network(s types.ServiceConfig) string {
	if s.NetworkMode != "" || len(s.Networks) == 0 {
		return ""
	}
	return s.NetworksByPriority()[0]
}

// network renders --network plus aliases; one-off containers (aliases=false)
// get no service alias, matching `docker compose run` without --use-aliases.
func (t *Translator) network(s types.ServiceConfig, aliases bool) []string {
	if s.NetworkMode == "none" {
		return []string{"--network", "none"}
	}
	key := Network(s)
	if key == "" {
		return nil
	}
	name := key
	if n, ok := t.Project.Networks[key]; ok && n.Name != "" {
		name = n.Name
	}
	a := argv{"--network", name}
	if !aliases {
		return a
	}
	names := []string{s.Name}
	if cfg := s.Networks[key]; cfg != nil {
		names = append(names, cfg.Aliases...)
	}
	slices.Sort(names)
	a.each("--network-alias", slices.Compact(names))
	return a
}

// Resources maps memory, CPU, shm, ulimits, stop settings and GPUs.
func Resources(s types.ServiceConfig) []string {
	var a argv
	mem, cpus := s.MemLimit, float64(s.CPUS)
	var lim *types.Resource
	if s.Deploy != nil {
		lim = s.Deploy.Resources.Limits
	}
	if lim != nil {
		if mem == 0 {
			mem = lim.MemoryBytes
		}
		if cpus == 0 {
			cpus = float64(lim.NanoCPUs)
		}
	}
	if mem > 0 {
		a.add("-m", Bytes(int64(mem)))
	}
	if cpus > 0 {
		a.add("--cpus", strconv.FormatFloat(cpus, 'f', -1, 32))
	}
	if s.ShmSize > 0 {
		a.add("--shm-size", Bytes(int64(s.ShmSize)))
	}
	for _, name := range slices.Sorted(maps.Keys(s.Ulimits)) {
		u := s.Ulimits[name]
		if u.Single != 0 {
			a.add("--ulimit", fmt.Sprintf("%s=%d", name, u.Single))
		} else {
			a.add("--ulimit", fmt.Sprintf("%s=%d:%d", name, u.Soft, u.Hard))
		}
	}
	a.opt("--stop-signal", s.StopSignal)
	if s.StopGracePeriod != nil {
		a.add("--stop-timeout", strconv.Itoa(int(time.Duration(*s.StopGracePeriod).Seconds())))
	}
	if wantsGPU(s) {
		a.add("--gpus", "all")
	}
	return a
}

func wantsGPU(s types.ServiceConfig) bool {
	if len(s.Gpus) > 0 {
		return true
	}
	if s.Deploy == nil || s.Deploy.Resources.Reservations == nil {
		return false
	}
	for _, d := range s.Deploy.Resources.Reservations.Devices {
		if slices.Contains(d.Capabilities, "gpu") {
			return true
		}
	}
	return false
}

// Bytes formats a byte count with the uppercase unit suffix wslc requires
// (512M is accepted, 512m is rejected).
func Bytes(n int64) string {
	for _, u := range []struct {
		suffix string
		size   int64
	}{{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}} {
		if n >= u.size && n%u.size == 0 {
			return strconv.FormatInt(n/u.size, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}

// Health maps a Compose healthcheck to wslc --health-* flags.
func Health(h *types.HealthCheckConfig) []string {
	if h == nil {
		return nil
	}
	if h.Disable || (len(h.Test) > 0 && h.Test[0] == "NONE") {
		return []string{"--no-healthcheck"}
	}
	var a argv
	if len(h.Test) > 1 {
		switch h.Test[0] {
		case "CMD-SHELL":
			a.add("--health-cmd", strings.Join(h.Test[1:], " "))
		case "CMD":
			a.add("--health-cmd", ShellJoin(h.Test[1:]))
		}
	}
	dur := func(flag string, d *types.Duration) {
		if d != nil {
			a.add(flag, time.Duration(*d).String())
		}
	}
	dur("--health-interval", h.Interval)
	dur("--health-timeout", h.Timeout)
	dur("--health-start-period", h.StartPeriod)
	if h.Retries != nil {
		a.add("--health-retries", strconv.FormatUint(*h.Retries, 10))
	}
	return a
}

// ShellJoin quotes words for /bin/sh so that exec-form health checks keep
// their argument boundaries when passed through --health-cmd.
func ShellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		if w != "" && !strings.ContainsAny(w, " \t\n'\"\\$`|&;<>()*?[]#~!{}") {
			out[i] = w
			continue
		}
		out[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
