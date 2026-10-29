package deploy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// An offline bundle is a .tar.gz holding everything a deploy downloads
// except container images: the playbook with its roles already installed,
// Python packages for Ansible, and the interzone binary itself.
//
//	interzone-bundle/
//	  bundle.json   metadata
//	  interzone     this binary
//	  playbook/     matrix-docker-ansible-deploy, roles in roles/galaxy
//	  wheels/       ansible + passlib for `pip install --no-index`

const (
	bundleRoot   = "interzone-bundle"
	bundleMarker = ".interzone-bundle.json"
)

type BundleInfo struct {
	Created   time.Time `json:"created"`
	Interzone string    `json:"interzone"`
	Playbook  string    `json:"playbook_commit"`
	Arch      string    `json:"arch"`
	Python    string    `json:"python"`
}

// bundled reports whether the playbook directory came from an offline bundle.
func bundled(playbookDir string) bool {
	_, err := os.Stat(filepath.Join(playbookDir, bundleMarker))
	return err == nil
}

func bundleWork() string { return filepath.Join(dataDir(), "bundle-work") }

func (r *Runner) wheels(ctx context.Context) error {
	r.begin(StepWheels)
	dir := filepath.Join(bundleWork(), "wheels")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := exec.LookPath("python3"); err != nil {
		r.note(StepWheels, "python3 isn't installed here, so the bundle won't include Ansible's Python packages")
		r.emit(Event{Step: StepWheels, Warn: true})
		return nil
	}
	// Use the system pip if there is one, else a throwaway venv's.
	pip := []string{"python3", "-m", "pip"}
	if exec.Command("python3", "-m", "pip", "--version").Run() != nil {
		venv := filepath.Join(bundleWork(), "venv")
		if err := r.exec(ctx, StepWheels, "", "python3", "-m", "venv", venv); err != nil {
			if ctx.Err() != nil {
				return err
			}
			r.note(StepWheels, "python3 has no pip and couldn't make a venv, so the bundle won't include Ansible's Python packages")
			r.emit(Event{Step: StepWheels, Warn: true})
			return nil
		}
		pip = []string{filepath.Join(venv, "bin", "pip")}
	}
	args := append(pip[1:], "download", "--dest", dir, "ansible", "passlib")
	if idx := r.cfg.Sources.PyPIIndex; idx != "" {
		args = append(args, "--index-url", idx)
	}
	if err := r.exec(ctx, StepWheels, "", pip[0], args...); err != nil {
		if ctx.Err() != nil {
			return err
		}
		r.note(StepWheels, "pip couldn't download the packages ("+err.Error()+"); the bundle won't include them")
		r.emit(Event{Step: StepWheels, Warn: true})
	}
	return nil
}

func (r *Runner) archive(ctx context.Context) error {
	r.begin(StepArchive)
	out := r.opts.Output
	if out == "" {
		out = fmt.Sprintf("interzone-bundle-%s.tar.gz", time.Now().Format("2006-01-02"))
	}
	out, _ = filepath.Abs(out)

	py, _ := exec.Command("python3", "-c", "import sys; print('%d.%d' % sys.version_info[:2])").Output()
	info := BundleInfo{
		Created:   time.Now().UTC(),
		Interzone: Version,
		Playbook:  r.gitHead(r.cfg.PlaybookDir),
		Arch:      runtime.GOOS + "/" + runtime.GOARCH,
		Python:    strings.TrimSpace(string(py)),
	}
	meta, _ := json.MarshalIndent(info, "", "  ")

	f, err := os.OpenFile(out+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer os.Remove(out + ".part")
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	add := func(name string, mode int64, body []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), ModTime: info.Created}); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	}
	if err := add(bundleRoot+"/bundle.json", 0o644, meta); err != nil {
		return err
	}
	if err := add(bundleRoot+"/README.txt", 0o644, []byte(bundleReadme)); err != nil {
		return err
	}
	if exe, err := os.Executable(); err == nil {
		if err := addFile(tw, exe, bundleRoot+"/interzone"); err != nil {
			return err
		}
	}
	r.note(StepArchive, "adding the playbook and roles")
	skip := func(rel string) bool {
		rel = filepath.ToSlash(rel)
		// Never ship the inventory: it holds this server's secrets.
		return rel == "inventory/hosts" || strings.HasPrefix(rel, "inventory/host_vars") ||
			strings.HasPrefix(rel, "inventory/interzone-") || strings.HasPrefix(rel, "inventory/.interzone-") ||
			rel == bundleMarker
	}
	if err := addTree(ctx, tw, r.cfg.PlaybookDir, bundleRoot+"/playbook", skip); err != nil {
		return err
	}
	if wd := filepath.Join(bundleWork(), "wheels"); hasWheels(wd) {
		r.note(StepArchive, "adding the Python packages")
		if err := addTree(ctx, tw, wd, bundleRoot+"/wheels", nil); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(out+".part", out); err != nil {
		return err
	}
	if st, err := os.Stat(out); err == nil {
		r.note(StepArchive, fmt.Sprintf("wrote %s (%.1f MB)", out, float64(st.Size())/1e6))
	}
	r.result.File = out
	return nil
}

const bundleReadme = `interzone offline bundle

Copy this file to the machine you deploy from (it needs no internet), then:

    tar xzf interzone-bundle-*.tar.gz
    ./interzone-bundle/interzone unbundle interzone-bundle
    ./interzone-bundle/interzone

Container images are not included: point interzone at a registry mirror
inside your network (Server step, "Container registry mirror").
`

func addFile(tw *tar.Writer, src, name string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: int64(st.Mode().Perm()), Size: st.Size(), ModTime: st.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func addTree(ctx context.Context, tw *tar.Writer, root, prefix string, skip func(string) bool) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := prefix + "/" + filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()})
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777, ModTime: info.ModTime()})
		case info.Mode().IsRegular():
			return addFile(tw, path, name)
		}
		return nil
	})
}

// Unbundle installs an offline bundle (the .tar.gz or its extracted
// directory) into the playbook and wheels directories, keeping any
// existing inventory. log receives progress lines.
func Unbundle(src, playbookDir string, log func(string)) (*BundleInfo, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	root := src
	if !st.IsDir() {
		tmp, err := os.MkdirTemp(dataDirFor(playbookDir), "unbundle-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		log("extracting " + src)
		if err := extract(src, tmp); err != nil {
			return nil, err
		}
		root = filepath.Join(tmp, bundleRoot)
	} else if _, err := os.Stat(filepath.Join(src, bundleRoot)); err == nil {
		root = filepath.Join(src, bundleRoot)
	}

	var info BundleInfo
	meta, err := os.ReadFile(filepath.Join(root, "bundle.json"))
	if err != nil {
		return nil, fmt.Errorf("%s is not an interzone bundle (no bundle.json)", src)
	}
	if err := json.Unmarshal(meta, &info); err != nil {
		return nil, err
	}

	// Swap the playbook in, carrying the existing inventory over.
	newPB := filepath.Join(root, "playbook")
	if _, err := os.Stat(filepath.Join(newPB, "setup.yml")); err != nil {
		return nil, fmt.Errorf("the bundle has no playbook")
	}
	if err := os.MkdirAll(filepath.Dir(playbookDir), 0o755); err != nil {
		return nil, err
	}
	staged := playbookDir + ".new"
	_ = os.RemoveAll(staged)
	if err := copyTree(newPB, staged); err != nil {
		return nil, err
	}
	if old := filepath.Join(playbookDir, "inventory"); exists(old) {
		log("keeping your existing inventory")
		_ = os.RemoveAll(filepath.Join(staged, "inventory"))
		if err := copyTree(old, filepath.Join(staged, "inventory")); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(staged, bundleMarker), meta, 0o644); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(playbookDir); err != nil {
		return nil, err
	}
	if err := os.Rename(staged, playbookDir); err != nil {
		return nil, err
	}
	log("playbook installed in " + playbookDir)

	if wd := filepath.Join(root, "wheels"); hasWheels(wd) {
		dst := filepath.Join(dataDirFor(playbookDir), "wheels")
		_ = os.RemoveAll(dst)
		if err := copyTree(wd, dst); err != nil {
			return nil, err
		}
		log("Python packages installed in " + dst)
	}
	return &info, nil
}

func dataDirFor(playbookDir string) string {
	d := filepath.Dir(playbookDir)
	_ = os.MkdirAll(d, 0o755)
	return d
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// extract unpacks a .tar.gz into dst, refusing entries that escape it.
// The writes go through an os.Root, so a link an earlier entry made can't
// carry a later one out of dst either.
func extract(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if _, ok := within(dst, h.Name); !ok {
			return fmt.Errorf("unsafe path in bundle: %s", h.Name)
		}
		name := filepath.Clean(h.Name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links must stay inside the bundle.
			if filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("unsafe link in bundle: %s", h.Name)
			}
			if _, ok := within(dst, filepath.Join(filepath.Dir(h.Name), h.Linkname)); !ok {
				return fmt.Errorf("unsafe link in bundle: %s", h.Name)
			}
			_ = root.MkdirAll(filepath.Dir(name), 0o755)
			if err := root.Symlink(h.Linkname, name); err != nil {
				return err
			}
		case tar.TypeReg:
			_ = root.MkdirAll(filepath.Dir(name), 0o755)
			out, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(h.Mode).Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}

func within(root, name string) (string, bool) {
	p := filepath.Join(root, name)
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	})
}
