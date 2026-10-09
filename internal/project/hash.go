package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/compose-spec/compose-go/v2/types"
)

// ServiceHash returns a stable digest of everything that shapes a service's
// containers. Fields that only influence scheduling or image preparation
// (scale, replicas, build, pull_policy, depends_on, profiles) are excluded so
// that changing them does not needlessly recreate running containers.
func ServiceHash(s types.ServiceConfig) (string, error) {
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
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
