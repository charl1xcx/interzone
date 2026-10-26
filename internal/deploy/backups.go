package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// ServerBackupDir is where scheduled backups are kept on the server.
const ServerBackupDir = "/matrix/interzone-backups"

// LocalBackup is a database dump on this computer.
type LocalBackup struct {
	Path string
	Time time.Time
	Size int64
}

// ListBackups returns the dumps of domain in BackupDir, newest first: those
// "Back up the database" made and those downloaded from the server.
func ListBackups(domain string) []LocalBackup {
	m, _ := filepath.Glob(filepath.Join(BackupDir(), domain+"-*.sql.gz"))
	var out []LocalBackup
	for _, f := range m {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
			out = append(out, LocalBackup{Path: f, Time: st.ModTime(), Size: st.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

const restorePrepPlay = `- name: Prepare to restore the Matrix Postgres database
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Look for the bundled Postgres data
      ansible.builtin.stat:
        path: /matrix/postgres/data
      register: interzone_pgdata

    - name: Stop if there is no bundled Postgres
      ansible.builtin.fail:
        msg: "There is no /matrix/postgres/data on the server, so there is no bundled Postgres to restore into."
      when: not interzone_pgdata.stat.isdir | default(false)

    - name: Upload the dump
      ansible.builtin.copy:
        src: "{{ interzone_restore_src }}"
        dest: /matrix/interzone-restore.sql.gz
        mode: "0600"

    - name: Stop every Matrix service
      ansible.builtin.shell: systemctl stop 'matrix-*.timer' 'matrix-*.service'
      changed_when: true

    - name: Empty the database directory
      ansible.builtin.shell: find /matrix/postgres/data -mindepth 1 -delete
      changed_when: true
`

const restoreFinishPlay = `- name: Finish restoring the Matrix Postgres database
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Forget one-time keys that were already handed out
      # Restoring an older dump would otherwise re-issue used keys and break
      # decryption (see the playbook's docs/importing-postgres.md).
      ansible.builtin.command: >-
        docker exec --env-file=/matrix/postgres/env-postgres-psql matrix-postgres
        psql -h matrix-postgres -d synapse -c 'TRUNCATE e2e_one_time_keys_json;'
      when: interzone_synapse | bool
      changed_when: true

    - name: Remove the uploaded dump
      ansible.builtin.file:
        path: /matrix/interzone-restore.sql.gz
        state: absent
`

// restore replaces the server's database with a dump. The plan backs up
// the current database first.
func (r *Runner) restore(ctx context.Context) error {
	r.begin(StepRestore)
	src := r.opts.Backup
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("no dump at %s", src)
	}
	inv := filepath.Join(r.cfg.PlaybookDir, "inventory")
	prep, finish := filepath.Join(inv, "interzone-restore.yml"), filepath.Join(inv, "interzone-restore-finish.yml")
	for f, body := range map[string]string{prep: restorePrepPlay, finish: restoreFinishPlay} {
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			return err
		}
	}
	dir := r.cfg.PlaybookDir
	play := func(args ...string) error {
		return r.exec(ctx, StepRestore, dir, "ansible-playbook", append([]string{"-i", "inventory/hosts"}, args...)...)
	}
	r.note(StepRestore, "stopping every service and emptying the database…")
	if err := play(prep, "-e", "interzone_restore_src="+src); err != nil {
		return err
	}
	r.note(StepRestore, "creating an empty database…")
	if err := play("setup.yml", "--tags=setup-postgres"); err != nil {
		return err
	}
	r.note(StepRestore, "importing "+filepath.Base(src)+"…")
	if err := play("setup.yml", "--tags=import-postgres",
		"-e", "server_path_postgres_dump=/matrix/interzone-restore.sql.gz",
		"-e", "postgres_default_import_database=matrix"); err != nil {
		return err
	}
	if err := play(finish, "-e", fmt.Sprintf("interzone_synapse=%t", r.cfg.Homeserver == "synapse")); err != nil {
		return err
	}
	r.note(StepRestore, "starting every service…")
	if err := play("setup.yml", "--tags=start"); err != nil {
		return err
	}
	r.result.File = src
	return nil
}

const schedulePlay = `- name: Schedule Matrix database backups
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Check the schedule
      ansible.builtin.command:
        argv: [systemd-analyze, calendar, "{{ interzone_when }}"]
      changed_when: false
      when: interzone_enabled | bool

    - name: Install the backup script
      ansible.builtin.copy:
        dest: /usr/local/bin/interzone-backup
        mode: "0755"
        content: |
          #!/bin/bash
          # Written by interzone: dumps the Matrix database and keeps the newest {{ interzone_keep }}.
          set -euo pipefail
          dir={{ interzone_dir }}
          mkdir -p "$dir"
          chmod 700 "$dir"
          f="$dir/$(date +%Y-%m-%d-%H%M).sql.gz"
          docker exec --env-file=/matrix/postgres/env-postgres-psql matrix-postgres \
            /usr/local/bin/pg_dumpall -h matrix-postgres | gzip -c > "$f.part"
          chmod 600 "$f.part"
          mv "$f.part" "$f"
          ls -1t "$dir"/*.sql.gz | tail -n +{{ interzone_keep | int + 1 }} | xargs -r rm -f --
      when: interzone_enabled | bool

    - name: Install the backup service
      ansible.builtin.copy:
        dest: /etc/systemd/system/interzone-backup.service
        mode: "0644"
        content: |
          [Unit]
          Description=Back up the Matrix database (interzone)
          Requires=matrix-postgres.service
          After=matrix-postgres.service

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/interzone-backup
      when: interzone_enabled | bool

    - name: Install the backup timer
      ansible.builtin.copy:
        dest: /etc/systemd/system/interzone-backup.timer
        mode: "0644"
        content: |
          [Unit]
          Description=Back up the Matrix database (interzone)

          [Timer]
          OnCalendar={{ interzone_when }}
          Persistent=true
          RandomizedDelaySec=10m

          [Install]
          WantedBy=timers.target
      when: interzone_enabled | bool

    - name: Start the timer
      ansible.builtin.systemd:
        name: interzone-backup.timer
        enabled: true
        state: restarted
        daemon_reload: true
      when: interzone_enabled | bool

    - name: Show the next run
      ansible.builtin.command: systemctl list-timers interzone-backup.timer --no-pager
      changed_when: false
      when: interzone_enabled | bool

    - name: Stop the timer
      ansible.builtin.systemd:
        name: interzone-backup.timer
        enabled: false
        state: stopped
      failed_when: false
      when: not interzone_enabled | bool

    - name: Remove the timer and service (existing backups are kept)
      ansible.builtin.file:
        path: "{{ item }}"
        state: absent
      loop:
        - /etc/systemd/system/interzone-backup.timer
        - /etc/systemd/system/interzone-backup.service
        - /usr/local/bin/interzone-backup
      when: not interzone_enabled | bool
`

var calendarSafe = regexp.MustCompile(`^[A-Za-z0-9 *:,./~-]+$`)

func (r *Runner) schedule(ctx context.Context) error {
	r.begin(StepSchedule)
	b := r.cfg.Backups
	if b.Enabled {
		if !calendarSafe.MatchString(b.When) {
			return fmt.Errorf("%q isn't a schedule systemd understands (try daily, weekly or *-*-* 03:30)", b.When)
		}
		if b.Keep < 1 {
			return fmt.Errorf("keep at least one backup")
		}
	}
	vars, _ := json.Marshal(map[string]any{"interzone_enabled": b.Enabled, "interzone_when": b.When,
		"interzone_keep": b.Keep, "interzone_dir": ServerBackupDir})
	play := filepath.Join(r.cfg.PlaybookDir, "inventory", "interzone-backup-schedule.yml")
	if err := os.WriteFile(play, []byte(schedulePlay), 0o644); err != nil {
		return err
	}
	return r.exec(ctx, StepSchedule, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", play, "-e", string(vars))
}

const fetchBackupsPlay = `- name: Download the Matrix database backups
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Find the backups
      ansible.builtin.find:
        paths: "{{ interzone_dir }}"
        patterns: "*.sql.gz"
      register: interzone_found

    - name: Copy them to this computer
      ansible.builtin.fetch:
        src: "{{ item.path }}"
        dest: "{{ interzone_dest }}/{{ interzone_domain }}-{{ item.path | basename }}"
        flat: true
      loop: "{{ interzone_found.files }}"
      loop_control:
        label: "{{ item.path | basename }}"
`

func (r *Runner) fetchBackups(ctx context.Context) error {
	r.begin(StepFetchBackups)
	if err := os.MkdirAll(BackupDir(), 0o700); err != nil {
		return err
	}
	before := len(ListBackups(r.cfg.Domain))
	vars, _ := json.Marshal(map[string]string{"interzone_dir": ServerBackupDir, "interzone_dest": BackupDir(), "interzone_domain": r.cfg.Domain})
	play := filepath.Join(r.cfg.PlaybookDir, "inventory", "interzone-backup-fetch.yml")
	if err := os.WriteFile(play, []byte(fetchBackupsPlay), 0o644); err != nil {
		return err
	}
	if err := r.exec(ctx, StepFetchBackups, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", play, "-e", string(vars)); err != nil {
		return err
	}
	all := ListBackups(r.cfg.Domain)
	for _, b := range all {
		_ = os.Chmod(b.Path, 0o600)
	}
	r.note(StepFetchBackups, fmt.Sprintf("%d new, %d in %s", len(all)-before, len(all), BackupDir()))
	r.result.File = BackupDir()
	return nil
}

// HumanSize formats a byte count, e.g. "12.3 MB".
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
