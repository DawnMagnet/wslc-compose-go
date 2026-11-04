package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/compose-spec/compose-go/v2/types"
)

// TranslateSchema is mixed into the config hash only when a service's wslc
// command differs from v0.1.5. Services whose command text is unchanged keep
// the historical digest, so an upgrade does not recreate them.
const TranslateSchema = 2

// ServiceHash returns a stable digest of everything that shapes a service's
// containers. eng selects whether newly translated, version-gated fields are
// part of the digest. Fields that only influence scheduling or image
// preparation (scale, replicas, build, pull_policy, depends_on, profiles) are
// excluded so that changing them does not needlessly recreate containers.
func ServiceHash(p *types.Project, s types.ServiceConfig, eng Engine) (string, error) {
	schema, tier := translationDelta(p, s, eng)
	s = stripHash(s)
	var (
		b   []byte
		err error
	)
	if schema {
		b, err = json.Marshal(struct {
			Schema  int                 `json:"schema"`
			Tier    string              `json:"tier,omitempty"`
			Service types.ServiceConfig `json:"service"`
		}{TranslateSchema, tier, s})
	} else {
		// Identical to v0.1.5: the service JSON alone.
		b, err = json.Marshal(s)
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func stripHash(s types.ServiceConfig) types.ServiceConfig {
	s.Build = nil
	s.PullPolicy = ""
	s.PullRefreshAfter = ""
	s.Scale = nil
	s.DependsOn = nil
	s.Profiles = nil
	if s.Deploy != nil {
		d := *s.Deploy
		d.Replicas = nil
		s.Deploy = &d
	}
	return s
}

// translationDelta reports whether s produces a different container command
// than v0.1.5 on eng. tier is set only when that difference depends on the
// wslc version (multi-network attach, static IPv4, secondary endpoint
// options). ip_range and build.secrets do not change the run command, and
// external secrets are not mounted, so none of those bump the digest.
// File-backed secrets and configs, and extra_hosts, change the command on
// every version, so they bump the schema without a tier. External configs
// are not mounted.
func translationDelta(p *types.Project, s types.ServiceConfig, eng Engine) (schema bool, tier string) {
	if mountedSecret(p, s) || mountedConfig(p, s) || len(s.ExtraHosts) > 0 {
		schema = true
	}
	if s.NetworkMode != "" {
		return schema, tier
	}
	keys := s.NetworksByPriority()
	if len(keys) > 1 && eng.MultiNetwork {
		schema, tier = true, "3.0.3"
	}
	for i, key := range keys {
		cfg := s.Networks[key]
		if cfg != nil && cfg.Ipv4Address != "" && eng.StaticIPv4 {
			schema, tier = true, "3.0.3"
		}
		if i > 0 && cfg != nil && eng.EndpointExtras && (len(cfg.LinkLocalIPs) > 0 || len(cfg.DriverOpts) > 0) {
			schema, tier = true, "3.0.3"
		}
	}
	return schema, tier
}

// mountedSecret reports whether s bind-mounts a secret. External secrets are
// skipped, matching v0.1.5's run command.
func mountedSecret(p *types.Project, s types.ServiceConfig) bool {
	for _, raw := range s.Secrets {
		ref := types.FileReferenceConfig(raw)
		if p == nil {
			return true
		}
		sec, ok := p.Secrets[ref.Source]
		if !ok || !bool(sec.External) {
			return true
		}
	}
	return false
}

// mountedConfig reports whether s bind-mounts a config. External configs are
// not mounted, so they do not change the run command.
func mountedConfig(p *types.Project, s types.ServiceConfig) bool {
	for _, raw := range s.Configs {
		ref := types.FileReferenceConfig(raw)
		if p == nil {
			return true
		}
		cfg, ok := p.Configs[ref.Source]
		if !ok || !bool(cfg.External) {
			return true
		}
	}
	return false
}
