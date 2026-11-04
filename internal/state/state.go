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
	"runtime"
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

// Prepare resolves every secret and config referenced by the selected
// services. When write is false (dry-run) it only computes paths and does
// not create directories or files. The bool reports whether dir did not
// exist before this call and write was requested, so a failed up can remove
// a directory it created.
func Prepare(dir string, p *types.Project, write bool) (*Prepared, bool, error) {
	created := false
	if write {
		fi, err := os.Lstat(dir)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			return nil, false, fmt.Errorf("state dir %s is a symlink", dir)
		case os.IsNotExist(err):
			created = true
		case err != nil:
			return nil, false, err
		}
		if err := ensureDir(dir); err != nil {
			return nil, created, err
		}
	}
	out := &Prepared{
		Dir:         dir,
		SecretFiles: map[string]string{},
		Mounts:      map[string][]Mount{},
	}
	w := &genWriter{dir: dir, write: write, done: map[string]string{}}
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
			m, path, err := w.resolve("secret", ref.Source, ref.Target, secrets[ref.Source], refExists(secrets, ref.Source), ref.Mode)
			if err != nil {
				return nil, created, fmt.Errorf("service %s: %w", svcName, err)
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
				_, path, err := w.resolve("secret", ref.Source, "", secrets[ref.Source], refExists(secrets, ref.Source), ref.Mode)
				if err != nil {
					return nil, created, fmt.Errorf("service %s: build secret: %w", svcName, err)
				}
				if path != "" {
					out.SecretFiles[ref.Source] = path
				}
			}
		}
		for _, raw := range s.Configs {
			ref := types.FileReferenceConfig(raw)
			m, _, err := w.resolve("config", ref.Source, ref.Target, configs[ref.Source], refExists(configs, ref.Source), ref.Mode)
			if err != nil {
				return nil, created, fmt.Errorf("service %s: %w", svcName, err)
			}
			if m != nil {
				mounts = append(mounts, *m)
			}
		}
		if err := uniqueTargets(svcName, s, mounts); err != nil {
			return nil, created, err
		}
		if len(mounts) > 0 {
			out.Mounts[svcName] = mounts
		}
	}
	return out, created, nil
}

func refExists(m map[string]types.FileObjectConfig, name string) bool {
	_, ok := m[name]
	return ok
}

// genWriter materialises each generated source at most once per Prepare.
type genWriter struct {
	dir   string
	write bool
	done  map[string]string
}

// resolve returns the container mount (nil when the object is external) and
// the host path of a secret (empty for external secrets and for configs).
func (w *genWriter) resolve(kind, source, target string, obj types.FileObjectConfig, ok bool, mode *types.FileMode) (*Mount, string, error) {
	if !ok {
		return nil, "", fmt.Errorf("%s %q is not defined", kind, source)
	}
	if bool(obj.External) {
		return nil, "", nil
	}
	host, err := w.hostPath(kind, source, obj, mode)
	if err != nil {
		return nil, "", err
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

func (w *genWriter) hostPath(kind, source string, obj types.FileObjectConfig, mode *types.FileMode) (string, error) {
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
	key := kind + "/" + source
	if path, ok := w.done[key]; ok {
		return path, nil
	}
	path := filepath.Join(w.dir, kind+"s", sanitize(source))
	if !w.write {
		w.done[key] = path
		return path, nil
	}
	if err := writeAtomic(path, []byte(obj.Content), fileMode(kind, mode)); err != nil {
		return "", err
	}
	w.done[key] = path
	return path, nil
}

func fileMode(kind string, mode *types.FileMode) os.FileMode {
	if mode != nil {
		return os.FileMode(*mode) & 0o777
	}
	if kind == "secret" {
		return 0o400
	}
	return 0o444
}

// writeAtomic replaces path without following a symlink and without
// truncating a read-only file in place. Each generated source is written
// once; a later Prepare creates a temp file, then renames it into place.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: refusing to follow symlink", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	if err := ensureDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".wslc-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := replaceFile(tmp, path); err != nil {
		return err
	}
	done = true
	return os.Chmod(path, mode)
}

func replaceFile(tmp, path string) error {
	if runtime.GOOS == "windows" {
		// os.Rename uses MoveFileEx(MOVEFILE_REPLACE_EXISTING). The target
		// must not be read-only or the replace is rejected. A sharing
		// violation (the file is mapped into a running container) is not
		// retried here; that needs a check on a real Windows machine.
		if _, err := os.Lstat(path); err == nil {
			_ = os.Chmod(path, 0o666)
		}
	}
	return os.Rename(tmp, path)
}

func ensureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0o700)
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
// On Windows, read-only files are made removable first. Symlinks are left
// alone so a link is not followed into another tree.
func Remove(dir string) error {
	if dir == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			_ = os.Chmod(path, 0o700)
			return nil
		})
	}
	err := os.RemoveAll(dir)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
