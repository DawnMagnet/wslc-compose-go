// Package state materialises Compose secrets and configs that are not already
// files. File sources are bind-mounted in place. environment: and content:
// sources are written under a per-project directory so wslc can mount them
// without the bytes appearing on a command line. External objects are skipped;
// callers warn about those separately.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// Mount is one read-only bind of a secret or config into a service.
type Mount struct {
	Source string
	Target string
}

// Prepared is the result of Prepare.
type Prepared struct {
	Dir         string
	SecretFiles map[string]string // secret name -> host path, for runtime mounts and build --secret
	Mounts      map[string][]Mount
}

// Prepare writes generated files and resolves every secret and config
// referenced by the selected services. dir is removed by Remove on down.
func Prepare(dir string, p *types.Project) (*Prepared, error) {
	out := &Prepared{
		Dir:         dir,
		SecretFiles: map[string]string{},
		Mounts:      map[string][]Mount{},
	}
	secrets := map[string]types.FileObjectConfig{}
	configs := map[string]types.FileObjectConfig{}
	for name, s := range p.Secrets {
		secrets[name] = types.FileObjectConfig(s)
	}
	for name, c := range p.Configs {
		configs[name] = types.FileObjectConfig(c)
	}
	for _, svcName := range p.ServiceNames() {
		s := p.Services[svcName]
		var mounts []Mount
		for _, raw := range s.Secrets {
			ref := types.FileReferenceConfig(raw)
			m, path, err := resolve("secret", ref.Source, ref.Target, secrets[ref.Source], refExists(secrets, ref.Source), ref.Mode, dir)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", svcName, err)
			}
			if path != "" {
				out.SecretFiles[ref.Source] = path
			}
			if m != nil {
				mounts = append(mounts, *m)
			}
		}
		if s.Build != nil {
			for _, raw := range s.Build.Secrets {
				ref := types.FileReferenceConfig(raw)
				_, path, err := resolve("secret", ref.Source, "", secrets[ref.Source], refExists(secrets, ref.Source), ref.Mode, dir)
				if err != nil {
					return nil, fmt.Errorf("service %s: build secret: %w", svcName, err)
				}
				if path != "" {
					out.SecretFiles[ref.Source] = path
				}
			}
		}
		for _, raw := range s.Configs {
			ref := types.FileReferenceConfig(raw)
			m, _, err := resolve("config", ref.Source, ref.Target, configs[ref.Source], refExists(configs, ref.Source), ref.Mode, dir)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", svcName, err)
			}
			if m != nil {
				mounts = append(mounts, *m)
			}
		}
		if err := uniqueTargets(svcName, s, mounts); err != nil {
			return nil, err
		}
		if len(mounts) > 0 {
			out.Mounts[svcName] = mounts
		}
	}
	return out, nil
}

func refExists(m map[string]types.FileObjectConfig, name string) bool {
	_, ok := m[name]
	return ok
}

// resolve returns the container mount (nil when the object is external) and
// the host path of a secret (empty for external secrets and for configs).
func resolve(kind, source, target string, obj types.FileObjectConfig, ok bool, mode *types.FileMode, dir string) (*Mount, string, error) {
	if !ok {
		return nil, "", fmt.Errorf("%s %q is not defined", kind, source)
	}
	if bool(obj.External) {
		return nil, "", nil
	}
	host, err := hostPath(kind, source, obj, dir)
	if err != nil {
		return nil, "", err
	}
	if obj.File == "" {
		if err := ApplyMode(host, true, mode); err != nil {
			return nil, "", err
		}
	}
	container, err := containerTarget(kind, source, target)
	if err != nil {
		return nil, "", err
	}
	mount := &Mount{Source: host, Target: container}
	if kind == "config" {
		return mount, "", nil
	}
	return mount, host, nil
}

func hostPath(kind, source string, obj types.FileObjectConfig, dir string) (string, error) {
	label := kind + " " + source
	if obj.File != "" {
		info, err := os.Stat(obj.File)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("%s: file not found: %s", label, obj.File)
		}
		return obj.File, nil
	}
	if obj.Content == "" && obj.Environment != "" {
		return "", fmt.Errorf("%s: environment variable %s is empty or unset", label, obj.Environment)
	}
	if obj.Content == "" && obj.Environment == "" {
		return "", fmt.Errorf("%s: needs file, environment, or content", label)
	}
	path := filepath.Join(dir, kind+"s", sanitize(source))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	mode := os.FileMode(0o444)
	if kind == "secret" {
		mode = 0o400
	}
	if err := os.WriteFile(path, []byte(obj.Content), mode); err != nil {
		return "", err
	}
	if err := os.Chmod(path, mode); err != nil {
		return "", err
	}
	return path, nil
}

// ApplyMode chmods a generated file to the Compose mode. File sources are
// left alone: changing them would rewrite the user's secret.
func ApplyMode(path string, generated bool, mode *types.FileMode) error {
	if !generated || mode == nil {
		return nil
	}
	return os.Chmod(path, os.FileMode(*mode)&0o777)
}

func containerTarget(kind, source, target string) (string, error) {
	name := target
	if name == "" {
		name = source
	}
	if strings.HasPrefix(name, "/") {
		return name, nil
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%s %q: invalid target %q", kind, source, name)
	}
	if kind == "secret" {
		return "/run/secrets/" + name, nil
	}
	return "/" + name, nil
}

func uniqueTargets(service string, s types.ServiceConfig, mounts []Mount) error {
	seen := map[string]bool{}
	for _, v := range s.Volumes {
		if v.Target != "" {
			seen[v.Target] = true
		}
	}
	for _, m := range mounts {
		if seen[m.Target] {
			return fmt.Errorf("service %s: %s is used by more than one volume, secret or config", service, m.Target)
		}
		seen[m.Target] = true
	}
	return nil
}

func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > 31 && r < 127 && !strings.ContainsRune(`/\:*?"<>|`, r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// Remove deletes the project state directory. A missing directory is not an error.
func Remove(dir string) error {
	if dir == "" {
		return nil
	}
	err := os.RemoveAll(dir)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
