package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"interzone/internal/config"
)

// Migration follows the playbook's docs/maintenance-migrating.md: stop and
// disable everything on the old server, copy /matrix over with ownership
// and permissions intact, point the inventory at the new server, recreate
// the matrix user, fix ownership and install. The old server keeps its
// data; only its services are disabled.

// sshArgs are the ssh options for a target.
func sshArgs(t config.Target) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new"}
	args = append(args, sshReuse()...)
	if t.Port != "" {
		args = append(args, "-p", t.Port)
	}
	if t.SSHKey != "" {
		args = append(args, "-i", config.ExpandHome(t.SSHKey))
	}
	return append(args, "--", t.User+"@"+t.Host) // --: a user or host starting with - isn't an option
}

// sshReuse keeps one connection per server open for a while and shares it
// between interzone's own ssh calls and Ansible's, so an action started
// shortly after another skips the TCP and key exchange round trips.
func sshReuse() []string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("interzone-ssh-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return []string{"-o", "ControlMaster=auto", "-o", "ControlPersist=10m", "-o", "ControlPath=" + filepath.Join(dir, "%C"),
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
}

// targetCmd runs a shell script on a target as the login user.
func targetCmd(ctx context.Context, t config.Target, script string) *exec.Cmd {
	if t.Mode == "ssh" {
		args := append([]string{"-o", "Compression=yes"}, sshArgs(t)...)
		return exec.CommandContext(ctx, "ssh", append(args, "sh -c "+shellQuote(script))...)
	}
	return exec.CommandContext(ctx, "sh", "-c", script)
}

// asRoot wraps a command so it runs as root. The sudo password is read from
// the first line of stdin, so the rest of stdin still reaches the command.
func asRoot(command string) string {
	return `read -r IZ_PW || true
if [ "$(id -u)" = 0 ]; then exec ` + command + `; fi
if [ -n "$IZ_PW" ]; then printf '%s\n' "$IZ_PW" | sudo -S -p '' -v || exit 1; fi
exec sudo -n ` + command
}

func targetName(t config.Target) string {
	if t.Mode == "ssh" {
		return t.User + "@" + t.Host
	}
	return "this machine"
}

// stopOld checks the new server before any downtime, then stops and
// disables every Matrix service on the old one.
func (r *Runner) stopOld(ctx context.Context) error {
	r.begin(StepStopOld)
	old, nt := r.cfg.Target, r.opts.NewTarget
	if nt.Mode == old.Mode && nt.Host == old.Host && (nt.Port == old.Port || nt.Mode == "local") {
		return fmt.Errorf("the new server is the same as the current one")
	}
	r.note(StepStopOld, "checking the new server ("+targetName(nt)+")…")
	newCfg := *r.cfg
	newCfg.Target = nt
	nf, err := probe(ctx, &newCfg)
	if err != nil {
		return fmt.Errorf("can't log in to the new server: %w", err)
	}
	if nf["sudo"] == "fail" {
		return fmt.Errorf("the new server needs a sudo password; enter it in the migration form")
	}
	of, err := probe(ctx, r.cfg)
	if err != nil {
		return fmt.Errorf("can't log in to the old server: %w", err)
	}
	if ociArch(of["arch"]) != ociArch(nf["arch"]) {
		return fmt.Errorf("the old server is %s and the new one %s: the database files can't move between CPU architectures. Back up, deploy fresh on the new server, then restore the backup there",
			of["arch"], nf["arch"])
	}
	out, err := r.output(ctx, nt, "test -e /matrix && echo exists || true")
	if err != nil {
		return fmt.Errorf("can't check the new server: %w", err)
	}
	if strings.Contains(out, "exists") {
		return fmt.Errorf("the new server already has a /matrix directory; move it away first so nothing is overwritten")
	}
	r.note(StepStopOld, fmt.Sprintf("new server: %s, %s, %s MB free", nf["os"], nf["arch"], nf["disk"]))

	r.note(StepStopOld, "stopping and disabling every Matrix service on "+targetName(old)+"…")
	return r.exec(ctx, StepStopOld, r.cfg.PlaybookDir, "ansible", "matrix_servers", "-i", "inventory/hosts", "--become",
		"-m", "ansible.builtin.shell", "-a",
		"cd /etc/systemd/system && systemctl disable --now 'matrix*' interzone-backup.timer 2>/dev/null; systemctl list-units --no-legend --plain --state=running 'matrix*'")
}

// output runs a short root command on a target.
func (r *Runner) output(ctx context.Context, t config.Target, command string) (string, error) {
	cmd := targetCmd(ctx, t, asRoot("sh -c "+shellQuote(command)))
	cmd.Stdin = strings.NewReader(t.BecomePass + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s", lastLine(stderr.String(), err))
	}
	return string(out), nil
}

type counter struct {
	r io.Reader
	n atomic.Int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// copyData streams /matrix from the old server to the new one through
// this computer, then switches the inventory to the new server.
func (r *Runner) copyData(ctx context.Context) error {
	r.begin(StepCopyData)
	old, nt := r.cfg.Target, r.opts.NewTarget
	r.note(StepCopyData, "copying /matrix from "+targetName(old)+" to "+targetName(nt)+"…")

	send := targetCmd(ctx, old, asRoot("tar -C / --numeric-owner -cpf - matrix"))
	send.Stdin = strings.NewReader(old.BecomePass + "\n")
	var sendErr, recvErr bytes.Buffer
	send.Stderr = &sendErr
	data, err := send.StdoutPipe()
	if err != nil {
		return err
	}
	cnt := &counter{r: data}
	recv := targetCmd(ctx, nt, asRoot("tar -C / --numeric-owner -xpf -"))
	recv.Stdin = io.MultiReader(strings.NewReader(nt.BecomePass+"\n"), cnt)
	recv.Stderr = &recvErr
	if err := recv.Start(); err != nil {
		return err
	}
	if err := send.Start(); err != nil {
		_ = recv.Process.Kill()
		_ = recv.Wait()
		return err
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				r.note(StepCopyData, "copied "+HumanSize(cnt.n.Load()))
			}
		}
	}()
	// The receiver first: send.Wait closes the pipe it's still reading.
	// If it gave up (a full disk), nothing reads the sender any more, so
	// stop it; otherwise take what's left after tar's end marker.
	rErr := recv.Wait()
	if rErr != nil {
		_ = send.Process.Kill()
	} else {
		_, _ = io.Copy(io.Discard, data)
	}
	sErr := send.Wait()
	close(done)
	if rErr != nil {
		// Either side can be the cause: a reader that failed first cuts
		// the archive short too.
		msg := "writing /matrix on the new server failed: " + lastLine(recvErr.String(), rErr)
		if strings.TrimSpace(sendErr.String()) != "" {
			msg += "; the old server said: " + lastLine(sendErr.String(), sErr)
		}
		return errors.New(msg)
	}
	if sErr != nil {
		return fmt.Errorf("reading /matrix on the old server failed: %s", lastLine(sendErr.String(), sErr))
	}
	r.note(StepCopyData, "copied "+HumanSize(cnt.n.Load())+" in total")

	// From here on everything runs against the new server.
	r.cfg.Target = nt
	hosts := filepath.Join(r.cfg.PlaybookDir, "inventory", "hosts")
	if err := config.WriteSecret(hosts, []byte(r.cfg.HostsINI())); err != nil {
		return err
	}
	r.note(StepCopyData, "the inventory now points at "+targetName(nt))
	r.emit(Event{Step: StepCopyData, Moved: true})

	r.note(StepCopyData, "creating the matrix user and fixing ownership…")
	if err := r.exec(ctx, StepCopyData, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", "setup.yml", "--tags=setup-system-user"); err != nil {
		return err
	}
	return r.exec(ctx, StepCopyData, r.cfg.PlaybookDir, "ansible", "matrix_servers", "-i", "inventory/hosts", "--become",
		"-m", "ansible.builtin.command", "-a", "chown -R matrix:matrix /matrix")
}
