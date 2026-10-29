package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// The server checks itself on a timer, as scheduled backups do, so alerts
// come even when this computer is off: failed matrix-* services, the
// homeserver not answering, a certificate close to expiring, a disk
// nearly full. It sends a message when the set of problems changes, so a
// problem is told once and so is its end.

const alertsScript = `#!/bin/bash
# Written by interzone: checks this Matrix server and sends a message when
# what's wrong changes. "interzone-alerts test" sends a test message.
set -uo pipefail
. "${IZ_ENV:-/etc/interzone-alerts.env}"
state=${IZ_STATE:-/var/lib/interzone-alerts.state}

send() {
  if [ "$IZ_JSON" = true ]; then
    printf '%s' "$1" | python3 -c 'import json, sys; m = sys.stdin.read(); print(json.dumps({"text": m, "body": m, "content": m}))' |
      curl -fsS -m 20 -o /dev/null -H 'Content-Type: application/json' --data-binary @- "$IZ_URL"
  else
    curl -fsS -m 20 -o /dev/null -H "Title: Matrix $IZ_DOMAIN" --data-binary "$1" "$IZ_URL"
  fi
}

if [ "${1:-}" = test ]; then
  send "interzone alerts are on for $IZ_DOMAIN: you'll hear here when something breaks, and when it's fixed."
  exit
fi

problems=()
while read -r unit _; do
  problems+=("$unit has failed")
done < <(systemctl list-units 'matrix-*.service' --state=failed --no-legend --plain 2>/dev/null)
if ! curl -ksS -m 15 -o /dev/null -f --resolve "$IZ_HOST:$IZ_PORT:127.0.0.1" "$IZ_SCHEME://$IZ_HOST:$IZ_PORT/_matrix/client/versions" 2>/dev/null; then
  problems+=("$IZ_SCHEME://$IZ_HOST doesn't answer")
fi
if [ "$IZ_SCHEME" = https ] && command -v openssl >/dev/null; then
  if ! openssl s_client -connect 127.0.0.1:443 -servername "$IZ_HOST" </dev/null 2>/dev/null | openssl x509 -noout -checkend 1209600 >/dev/null 2>&1; then
    problems+=("the certificate of $IZ_HOST expires within 14 days, or can't be read")
  fi
fi
while read -r use mount; do
  if [ "${use%?}" -ge 90 ]; then
    problems+=("$mount is $use full")
  fi
done < <(df -P / /matrix 2>/dev/null | awk 'NR > 1 && !seen[$6]++ { print $5, $6 }')

now=$(printf '%s\n' "${problems[@]+"${problems[@]}"}")
before=$(cat "$state" 2>/dev/null || true)
[ "$now" = "$before" ] && exit 0
if [ -z "$now" ]; then
  send "✓ $IZ_DOMAIN: everything is fine again." || exit 1
else
  msg="✗ $IZ_DOMAIN:"
  for p in "${problems[@]}"; do msg+=$'\n'"• $p"; done
  send "$msg" || exit 1
fi
printf '%s' "$now" > "$state"
`

const alertsPlay = `- name: Set up alerts
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: Check the schedule
      ansible.builtin.command:
        argv: [systemd-analyze, calendar, "{{ interzone_every }}"]
      changed_when: false
      when: interzone_enabled | bool

    - name: Install curl and openssl
      ansible.builtin.package:
        name: [curl, openssl]
        state: present
      when: interzone_enabled | bool

    - name: Write the settings
      ansible.builtin.copy:
        dest: /etc/interzone-alerts.env
        mode: "0600"
        content: |
          IZ_DOMAIN={{ interzone_domain | quote }}
          IZ_HOST={{ interzone_host | quote }}
          IZ_SCHEME={{ interzone_scheme | quote }}
          IZ_PORT={{ interzone_port | quote }}
          IZ_URL={{ interzone_url | quote }}
          IZ_JSON={{ interzone_json | bool | lower }}
      no_log: true
      when: interzone_enabled | bool

    - name: Install the check
      ansible.builtin.copy:
        dest: /usr/local/bin/interzone-alerts
        mode: "0755"
        content: "{{ interzone_script }}"
      when: interzone_enabled | bool

    - name: Install the service and timer
      ansible.builtin.copy:
        dest: "/etc/systemd/system/{{ item.name }}"
        mode: "0644"
        content: "{{ item.content }}"
      loop:
        - name: interzone-alerts.service
          content: |
            [Unit]
            Description=Check the Matrix server and send alerts (interzone)

            [Service]
            Type=oneshot
            ExecStart=/usr/local/bin/interzone-alerts
        - name: interzone-alerts.timer
          content: |
            [Unit]
            Description=Check the Matrix server (interzone)

            [Timer]
            OnCalendar={{ interzone_every }}
            Persistent=true

            [Install]
            WantedBy=timers.target
      loop_control:
        label: "{{ item.name }}"
      when: interzone_enabled | bool

    - name: Send a test message
      ansible.builtin.command: /usr/local/bin/interzone-alerts test
      changed_when: false
      when: interzone_enabled | bool

    - name: Start the timer
      ansible.builtin.systemd:
        name: interzone-alerts.timer
        enabled: true
        state: restarted
        daemon_reload: true
      when: interzone_enabled | bool

    - name: Stop the timer
      ansible.builtin.systemd:
        name: interzone-alerts.timer
        enabled: false
        state: stopped
      failed_when: false
      when: not interzone_enabled | bool

    - name: Remove the timer, service and check
      ansible.builtin.file:
        path: "{{ item }}"
        state: absent
      loop:
        - /etc/systemd/system/interzone-alerts.timer
        - /etc/systemd/system/interzone-alerts.service
        - /usr/local/bin/interzone-alerts
        - /etc/interzone-alerts.env
        - /var/lib/interzone-alerts.state
      when: not interzone_enabled | bool
`

// AlertsURLProblem says what's wrong with an alerts URL, or "".
func AlertsURLProblem(u string) string {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
		return "The alerts address must be an http(s):// URL, e.g. https://ntfy.sh/a-long-random-topic."
	}
	return ""
}

func (r *Runner) alerts(ctx context.Context) error {
	r.begin(StepAlerts)
	a := r.cfg.Alerts
	if a.Enabled {
		if !calendarSafe.MatchString(a.Every) {
			return fmt.Errorf("%q isn't a schedule systemd understands (try *:0/5 for every 5 minutes)", a.Every)
		}
		if p := AlertsURLProblem(a.URL); p != "" {
			return fmt.Errorf("%s", p)
		}
	}
	scheme, port := "https", "443"
	if r.cfg.SSL.Mode == "none" {
		scheme, port = "http", "80"
	}
	// The URL may hold a secret topic or token, so it goes in a file, not
	// on the command line.
	vars, _ := json.Marshal(map[string]any{"interzone_enabled": a.Enabled, "interzone_every": a.Every,
		"interzone_url": a.URL, "interzone_json": a.JSON, "interzone_domain": r.cfg.Domain,
		"interzone_host": r.cfg.HostName(), "interzone_scheme": scheme, "interzone_port": port,
		"interzone_script": alertsScript})
	dir := filepath.Join(r.cfg.PlaybookDir, "inventory")
	varsFile := filepath.Join(dir, "interzone-alerts-vars.json")
	if err := os.WriteFile(varsFile, vars, 0o600); err != nil {
		return err
	}
	defer os.Remove(varsFile)
	play := filepath.Join(dir, "interzone-alerts.yml")
	if err := os.WriteFile(play, []byte(alertsPlay), 0o644); err != nil {
		return err
	}
	return r.exec(ctx, StepAlerts, r.cfg.PlaybookDir, "ansible-playbook", "-i", "inventory/hosts", play, "-e", "@"+varsFile)
}
