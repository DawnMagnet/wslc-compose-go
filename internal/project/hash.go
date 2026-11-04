package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/compose-spec/compose-go/v2/types"
)

// TranslateSchema is mixed into every config hash. Bump it when an unchanged
// service starts producing a different wslc command sequence, so containers
// created by an older wslc-compose (v0.1.5 hashed the service alone) are
// recreated on the next up.
const TranslateSchema = 2

// ServiceHash returns a stable digest of everything that shapes a service's
// containers. tier is the engine capability tier (Engine.Tier); a container
// created for an older wslc is recreated after the binary is upgraded.
// Fields that only influence scheduling or image preparation
// (scale, replicas, build, pull_policy, depends_on, profiles) are excluded so
// that changing them does not needlessly recreate running containers.
func ServiceHash(s types.ServiceConfig, tier string) (string, error) {
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
	if tier == "" {
		tier = "legacy"
	}
	b, err := json.Marshal(struct {
		Schema  int                 `json:"schema"`
		Tier    string              `json:"tier"`
		Service types.ServiceConfig `json:"service"`
	}{TranslateSchema, tier, s})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
