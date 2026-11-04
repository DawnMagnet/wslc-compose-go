package project

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

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
	{"build.ssh", "wslc build has no --ssh; ignored", func(s *types.ServiceConfig) bool {
		return s.Build != nil && len(s.Build.SSH) > 0
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

// Engine describes the wslc binary that will run the plan. Zero means the
// probed build predates every gated feature (or the version could not be read).
type Engine struct {
	MultiNetwork   bool // create + network connect + start
	StaticIPv4     bool // --ip on run/create and network connect
	IPRange        bool // network create --ip-range
	EndpointExtras bool // --link-local-ip and --driver-opt on network connect
	BuildSecret    bool // wslc build --secret
	HostGateway    bool // system session run + iptables for host-gateway
}

// LatestEngine is a current wslc (3.0.3.0 feature floors).
func LatestEngine() Engine {
	return Engine{MultiNetwork: true, StaticIPv4: true, IPRange: true, EndpointExtras: true, BuildSecret: true, HostGateway: true}
}

// Check reports fields the latest known wslc still cannot honor.
func Check(p *types.Project) []Issue { return CheckEngine(p, LatestEngine()) }

// CheckEngine inspects the (already selected) project and reports every field
// that this wslc build will drop or approximate.
func CheckEngine(p *types.Project, eng Engine) []Issue {
	var out []Issue
	for _, name := range p.ServiceNames() {
		s := p.Services[name]
		for _, r := range serviceRules {
			if r.set(&s) {
				out = append(out, Issue{Warn, name, r.field, r.detail})
			}
		}
		out = append(out, checkDynamic(name, &s, eng)...)
		out = append(out, checkFileRefs(name, "secret", secretRefs(s.Secrets), secretObjects(p), eng)...)
		out = append(out, checkFileRefs(name, "config", configRefs(s.Configs), configObjects(p), eng)...)
		if s.Build != nil {
			out = append(out, checkFileRefs(name, "build.secret", secretRefs(s.Build.Secrets), secretObjects(p), eng)...)
			if len(s.Build.Secrets) > 0 && !eng.BuildSecret {
				out = append(out, Issue{Warn, name, "build.secrets", "this wslc build has no build --secret; ignored"})
			}
		}
	}
	for _, name := range sortedKeys(p.Networks) {
		n := p.Networks[name]
		if n.Driver != "" && n.Driver != "bridge" && !bool(n.External) {
			out = append(out, Issue{Warn, "", "networks." + name + ".driver", fmt.Sprintf("driver %q is passed through; wslc may reject it", n.Driver)})
		}
		if n.EnableIPv6 != nil && *n.EnableIPv6 {
			out = append(out, Issue{Warn, "", "networks." + name + ".enable_ipv6", "wslc networks are IPv4-only; ignored"})
		}
		if n.EnableIPv4 != nil && !*n.EnableIPv4 {
			out = append(out, Issue{Warn, "", "networks." + name + ".enable_ipv4", ignored})
		}
		if n.Attachable {
			out = append(out, Issue{Warn, "", "networks." + name + ".attachable", ignored})
		}
		if n.Ipam.Driver != "" && n.Ipam.Driver != "default" {
			out = append(out, Issue{Warn, "", "networks." + name + ".ipam.driver", "custom IPAM drivers are ignored"})
		}
		if len(n.Ipam.Options) > 0 {
			out = append(out, Issue{Warn, "", "networks." + name + ".ipam.options", ignored})
		}
		if len(n.Ipam.Config) > 1 {
			out = append(out, Issue{Warn, "", "networks." + name + ".ipam.config", "wslc accepts one subnet per network; only the first config entry is applied"})
		}
		for _, pool := range n.Ipam.Config {
			if pool == nil {
				continue
			}
			if pool.IPRange != "" && !eng.IPRange {
				out = append(out, Issue{Warn, "", "networks." + name + ".ipam.ip_range", "this wslc build has no --ip-range; ignored"})
			}
			if len(pool.AuxiliaryAddresses) > 0 {
				out = append(out, Issue{Warn, "", "networks." + name + ".ipam.aux_addresses", ignored})
			}
		}
	}
	for _, name := range sortedKeys(p.Volumes) {
		if v := p.Volumes[name]; v.Driver != "" && v.Driver != "local" || len(v.DriverOpts) > 0 {
			out = append(out, Issue{Warn, "", "volumes." + name + ".driver", "volume drivers/options are ignored"})
		}
	}
	return out
}

func secretRefs(in []types.ServiceSecretConfig) []types.FileReferenceConfig {
	out := make([]types.FileReferenceConfig, len(in))
	for i, r := range in {
		out[i] = types.FileReferenceConfig(r)
	}
	return out
}

func configRefs(in []types.ServiceConfigObjConfig) []types.FileReferenceConfig {
	out := make([]types.FileReferenceConfig, len(in))
	for i, r := range in {
		out[i] = types.FileReferenceConfig(r)
	}
	return out
}

func secretObjects(p *types.Project) map[string]types.FileObjectConfig {
	out := make(map[string]types.FileObjectConfig, len(p.Secrets))
	for k, v := range p.Secrets {
		out[k] = types.FileObjectConfig(v)
	}
	return out
}

func configObjects(p *types.Project) map[string]types.FileObjectConfig {
	out := make(map[string]types.FileObjectConfig, len(p.Configs))
	for k, v := range p.Configs {
		out[k] = types.FileObjectConfig(v)
	}
	return out
}

func checkFileRefs(service, kind string, refs []types.FileReferenceConfig, objs map[string]types.FileObjectConfig, eng Engine) []Issue {
	var out []Issue
	for _, ref := range refs {
		obj, ok := objs[ref.Source]
		field := kind + "s"
		if kind == "build.secret" {
			field = "build.secrets"
		}
		if !ok {
			out = append(out, Issue{Warn, service, field, fmt.Sprintf("%s %q is not defined", kind, ref.Source)})
			continue
		}
		if bool(obj.External) {
			out = append(out, Issue{Warn, service, field, fmt.Sprintf("external %s %q is not provided by wslc; the mount is skipped", kind, ref.Source)})
		}
		if ref.UID != "" || ref.GID != "" {
			out = append(out, Issue{Warn, service, field + "." + ref.Source + ".uid", "wslc bind mounts cannot set uid or gid; ignored"})
		}
		// mode on a file: source is the host file's mode. Generated
		// environment/content files are chmod'd to the requested mode.
		if ref.Mode != nil && obj.File != "" && !bool(obj.External) {
			out = append(out, Issue{Warn, service, field + "." + ref.Source + ".mode", "mode is not applied to file: sources; the host file's mode is used"})
		}
		if obj.TemplateDriver != "" {
			out = append(out, Issue{Warn, service, field + "." + ref.Source + ".template_driver", ignored})
		}
		if obj.Driver != "" {
			out = append(out, Issue{Warn, service, field + "." + ref.Source + ".driver", "secret/config drivers are ignored"})
		}
		_ = eng
	}
	return out
}

func checkDynamic(name string, s *types.ServiceConfig, eng Engine) []Issue {
	var out []Issue
	add := func(l Level, field, detail string) { out = append(out, Issue{l, name, field, detail}) }
	switch mode := s.NetworkMode; {
	case mode == "" || mode == "none" || mode == "bridge":
	case mode == "host":
		add(Warn, "network_mode", "host networking is rejected by wslc; the container joins wslc's default network instead")
	default:
		add(Warn, "network_mode", fmt.Sprintf("%q is not supported; ignored", mode))
	}
	keys := s.NetworksByPriority()
	if len(keys) > 1 && !eng.MultiNetwork {
		add(Warn, "networks", fmt.Sprintf("this wslc build attaches one network per container; using %q", keys[0]))
	}
	primary := ""
	if len(keys) > 0 && s.NetworkMode == "" {
		primary = keys[0]
	}
	for _, net := range keys {
		cfg := s.Networks[net]
		if cfg == nil {
			continue
		}
		if cfg.Ipv4Address != "" && !eng.StaticIPv4 {
			add(Warn, "networks."+net+".ipv4_address", "this wslc build has no --ip; static IPv4 is ignored")
		}
		if cfg.Ipv6Address != "" {
			add(Warn, "networks."+net+".ipv6_address", "wslc networks are IPv4-only; ipv6_address is ignored")
		}
		if cfg.MacAddress != "" {
			add(Warn, "networks."+net+".mac_address", ignored)
		}
		if cfg.GatewayPriority != 0 {
			add(Warn, "networks."+net+".gw_priority", ignored)
		}
		if cfg.InterfaceName != "" {
			add(Warn, "networks."+net+".interface_name", ignored)
		}
		extras := len(cfg.LinkLocalIPs) > 0 || len(cfg.DriverOpts) > 0
		switch {
		case extras && !eng.EndpointExtras:
			add(Warn, "networks."+net+".link_local_ips", "endpoint link-local IPs and driver_opts need a newer wslc; ignored")
		case extras && net == primary:
			add(Warn, "networks."+net+".driver_opts", "primary endpoint link-local IPs and driver_opts have no wslc run flag; ignored (they are applied when the network is added with network connect)")
		}
	}
	if len(s.ExtraHosts) > 0 {
		add(Info, "extra_hosts", "applied with wslc exec after start; the entrypoint may not see the new /etc/hosts lines")
	}
	if usesHostGateway(s.ExtraHosts) && !eng.HostGateway {
		add(Warn, "extra_hosts.host-gateway", "published-port loopback needs a newer wslc (system session run); /etc/hosts is still updated")
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

func usesHostGateway(h types.HostsList) bool {
	for _, addrs := range h {
		for _, addr := range addrs {
			if strings.EqualFold(strings.TrimSpace(addr), "host-gateway") {
				return true
			}
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
