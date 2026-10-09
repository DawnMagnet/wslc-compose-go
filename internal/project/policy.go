package project

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
)

// Level classifies how far a Compose field is from what wslc can express.
type Level int

const (
	// Info marks fields that are mapped but whose wslc behavior should be verified.
	Info Level = iota
	// Warn marks fields that are ignored or approximated; --strict turns these into errors.
	Warn
)

// Issue is one Compose feature that wslc-compose cannot honor exactly.
type Issue struct {
	Level   Level
	Service string // empty for project-level resources
	Field   string
	Detail  string
}

func (i Issue) String() string {
	scope := i.Service
	if scope == "" {
		scope = "project"
	}
	tag := "WARN"
	if i.Level == Info {
		tag = "INFO"
	}
	return fmt.Sprintf("%s [%s] %s: %s", tag, scope, i.Field, i.Detail)
}

// ErrStrict is returned by Enforce when --strict rejects the model.
var ErrStrict = errors.New("compose file uses features wslc cannot honor (--strict)")

type rule struct {
	field  string
	detail string
	set    func(s *types.ServiceConfig) bool
}

const ignored = "not supported by wslc run; ignored"

// serviceRules lists fields that wslc has no flag for.
var serviceRules = []rule{
	{"restart", "wslc has no restart policies; ignored (use a scheduled task)", func(s *types.ServiceConfig) bool { return s.Restart != "" && s.Restart != types.RestartPolicyNo }},
	{"deploy.restart_policy", "wslc has no restart policies; ignored", func(s *types.ServiceConfig) bool { return s.Deploy != nil && s.Deploy.RestartPolicy != nil }},
	{"privileged", ignored, func(s *types.ServiceConfig) bool { return s.Privileged }},
	{"cap_add", ignored, func(s *types.ServiceConfig) bool { return len(s.CapAdd) > 0 }},
	{"cap_drop", ignored, func(s *types.ServiceConfig) bool { return len(s.CapDrop) > 0 }},
	{"devices", "no generic device passthrough in wslc (only --gpus); ignored", func(s *types.ServiceConfig) bool { return len(s.Devices) > 0 }},
	{"device_cgroup_rules", ignored, func(s *types.ServiceConfig) bool { return len(s.DeviceCgroupRules) > 0 }},
	{"security_opt", ignored, func(s *types.ServiceConfig) bool { return len(s.SecurityOpt) > 0 }},
	{"sysctls", ignored, func(s *types.ServiceConfig) bool { return len(s.Sysctls) > 0 }},
	{"extra_hosts", ignored, func(s *types.ServiceConfig) bool { return len(s.ExtraHosts) > 0 }},
	{"secrets", "secrets are not implemented by wslc; ignored", func(s *types.ServiceConfig) bool { return len(s.Secrets) > 0 }},
	{"configs", "configs are not implemented by wslc; ignored", func(s *types.ServiceConfig) bool { return len(s.Configs) > 0 }},
	{"logging", "wslc has no log drivers; ignored", func(s *types.ServiceConfig) bool { return s.Logging != nil || s.LogDriver != "" }},
	{"platform", "wslc runs host-architecture images only; ignored", func(s *types.ServiceConfig) bool { return s.Platform != "" }},
	{"init", ignored, func(s *types.ServiceConfig) bool { return s.Init != nil && *s.Init }},
	{"read_only", ignored, func(s *types.ServiceConfig) bool { return s.ReadOnly }},
	{"ipc", ignored, func(s *types.ServiceConfig) bool { return s.Ipc != "" }},
	{"pid", ignored, func(s *types.ServiceConfig) bool { return s.Pid != "" }},
	{"uts", ignored, func(s *types.ServiceConfig) bool { return s.Uts != "" }},
	{"userns_mode", ignored, func(s *types.ServiceConfig) bool { return s.UserNSMode != "" }},
	{"group_add", ignored, func(s *types.ServiceConfig) bool { return len(s.GroupAdd) > 0 }},
	{"cgroup", ignored, func(s *types.ServiceConfig) bool { return s.Cgroup != "" || s.CgroupParent != "" }},
	{"runtime", ignored, func(s *types.ServiceConfig) bool { return s.Runtime != "" }},
	{"isolation", ignored, func(s *types.ServiceConfig) bool { return s.Isolation != "" }},
	{"mac_address", ignored, func(s *types.ServiceConfig) bool { return s.MacAddress != "" }},
	{"links", "use networks + aliases; ignored", func(s *types.ServiceConfig) bool { return len(s.Links) > 0 || len(s.ExternalLinks) > 0 }},
	{"volumes_from", ignored, func(s *types.ServiceConfig) bool { return len(s.VolumesFrom) > 0 }},
	{"storage_opt", ignored, func(s *types.ServiceConfig) bool { return len(s.StorageOpt) > 0 }},
	{"oom_kill_disable/oom_score_adj", ignored, func(s *types.ServiceConfig) bool { return s.OomKillDisable || s.OomScoreAdj != 0 }},
	{"pids_limit", ignored, func(s *types.ServiceConfig) bool { return s.PidsLimit != 0 }},
	{"blkio_config", ignored, func(s *types.ServiceConfig) bool { return s.BlkioConfig != nil }},
	{"cpu_shares/cpu_quota/cpuset", "only `cpus` maps to wslc; other CPU knobs ignored", func(s *types.ServiceConfig) bool {
		return s.CPUShares != 0 || s.CPUQuota != 0 || s.CPUPeriod != 0 || s.CPUSet != "" || s.CPUCount != 0 || s.CPUPercent != 0
	}},
	{"mem_reservation/memswap_limit", "only mem_limit maps to wslc; ignored", func(s *types.ServiceConfig) bool {
		return s.MemReservation != 0 || s.MemSwapLimit != 0 || s.MemSwappiness != 0
	}},
	{"post_start/pre_stop", "lifecycle hooks are not implemented; ignored", func(s *types.ServiceConfig) bool {
		return len(s.PostStart) > 0 || len(s.PreStop) > 0 || len(s.PreStart) > 0
	}},
	{"develop", "watch mode is not implemented; ignored", func(s *types.ServiceConfig) bool { return s.Develop != nil }},
	{"provider", "provider services are not implemented; ignored", func(s *types.ServiceConfig) bool { return s.Provider != nil }},
	{"models", "AI models are not implemented; ignored", func(s *types.ServiceConfig) bool { return len(s.Models) > 0 }},
	{"use_api_socket", "wslc exposes no Docker socket; ignored", func(s *types.ServiceConfig) bool { return s.UseAPISocket }},
	{"build.secrets/ssh", "wslc build has no --secret/--ssh; ignored", func(s *types.ServiceConfig) bool {
		return s.Build != nil && (len(s.Build.Secrets) > 0 || len(s.Build.SSH) > 0)
	}},
	{"build.platforms/cache_from/cache_to", "wslc build has no buildx features; ignored", func(s *types.ServiceConfig) bool {
		return s.Build != nil && (len(s.Build.Platforms) > 0 || len(s.Build.CacheFrom) > 0 || len(s.Build.CacheTo) > 0)
	}},
	{"build.dockerfile_inline", "inline Dockerfiles are not supported; build will fail", func(s *types.ServiceConfig) bool {
		return s.Build != nil && s.Build.DockerfileInline != ""
	}},
	{"build.additional_contexts/network/extra_hosts", "not supported by wslc build; ignored", func(s *types.ServiceConfig) bool {
		return s.Build != nil && (len(s.Build.AdditionalContexts) > 0 || s.Build.Network != "" || len(s.Build.ExtraHosts) > 0)
	}},
	{"healthcheck.start_interval", "no wslc flag; ignored", func(s *types.ServiceConfig) bool { return s.HealthCheck != nil && s.HealthCheck.StartInterval != nil }},
}

// Check inspects the (already selected) project and reports every field that
// wslc-compose will drop or approximate.
func Check(p *types.Project) []Issue {
	var out []Issue
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		for _, r := range serviceRules {
			if r.set(&s) {
				out = append(out, Issue{Warn, name, r.field, r.detail})
			}
		}
		out = append(out, checkDynamic(name, &s)...)
	}
	for _, name := range sortedKeys(p.Networks) {
		n := p.Networks[name]
		if n.Driver != "" && n.Driver != "bridge" && !bool(n.External) {
			out = append(out, Issue{Warn, "", "networks." + name + ".driver", fmt.Sprintf("driver %q is passed through; wslc may reject it", n.Driver)})
		}
		if n.EnableIPv6 != nil && *n.EnableIPv6 {
			out = append(out, Issue{Warn, "", "networks." + name + ".enable_ipv6", ignored})
		}
	}
	for _, name := range sortedKeys(p.Volumes) {
		if v := p.Volumes[name]; v.Driver != "" && v.Driver != "local" || len(v.DriverOpts) > 0 {
			out = append(out, Issue{Warn, "", "volumes." + name + ".driver", "volume drivers/options are ignored"})
		}
	}
	if len(p.Secrets) > 0 || len(p.Configs) > 0 {
		out = append(out, Issue{Warn, "", "secrets/configs", "top-level secrets and configs are ignored"})
	}
	return out
}

func checkDynamic(name string, s *types.ServiceConfig) []Issue {
	var out []Issue
	add := func(l Level, field, detail string) { out = append(out, Issue{l, name, field, detail}) }
	switch mode := s.NetworkMode; {
	case mode == "" || mode == "none" || mode == "bridge":
	case mode == "host":
		add(Warn, "network_mode", "host networking is rejected by wslc; the container joins wslc's default network instead")
	default:
		add(Warn, "network_mode", fmt.Sprintf("%q is not supported; ignored", mode))
	}
	if len(s.Networks) > 1 {
		add(Warn, "networks", fmt.Sprintf("wslc attaches one network per container; using %q", s.NetworksByPriority()[0]))
	}
	for net, cfg := range s.Networks {
		if cfg != nil && (cfg.Ipv4Address != "" || cfg.Ipv6Address != "" || cfg.MacAddress != "") {
			add(Warn, "networks."+net+".ipv4_address", "static addresses are ignored")
		}
	}
	for _, v := range s.Volumes {
		switch v.Type {
		case types.VolumeTypeBind, types.VolumeTypeVolume, types.VolumeTypeTmpfs:
		default:
			add(Warn, "volumes", fmt.Sprintf("volume type %q is not supported; ignored", v.Type))
		}
		if v.Volume != nil && (v.Volume.NoCopy || v.Volume.Subpath != "") {
			add(Warn, "volumes", "volume nocopy/subpath options are ignored")
		}
	}
	for _, p := range s.Ports {
		if p.Mode == "host" {
			add(Info, "ports", "mode: host is treated like a normal published port")
		}
	}
	if s.HealthCheck != nil && !s.HealthCheck.Disable {
		add(Info, "healthcheck", "mapped to wslc --health-* flags; verify with `wslc run --help` on your build")
	}
	for dep, d := range s.DependsOn {
		if d.Condition == types.ServiceConditionHealthy {
			add(Info, "depends_on."+dep, "service_healthy waits by polling `wslc inspect`; falls back to service_started if no health state is reported")
		}
	}
	if s.Deploy != nil && s.Deploy.Resources.Reservations != nil && s.Deploy.Resources.Reservations.MemoryBytes != 0 {
		add(Warn, "deploy.resources.reservations", "only limits map to wslc; reservations ignored")
	}
	return out
}

// Enforce prints issues to w. In strict mode any Warn-level issue aborts.
func Enforce(issues []Issue, strict bool, w io.Writer) error {
	failed := false
	for _, i := range issues {
		fmt.Fprintln(w, i)
		failed = failed || i.Level == Warn
	}
	if strict && failed {
		return ErrStrict
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
