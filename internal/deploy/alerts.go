package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"interzone/internal/catalog"
	"interzone/internal/config"
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
set -a; . "${IZ_ENV:-/etc/interzone-alerts.env}"; set +a
state=${IZ_STATE:-/var/lib/interzone-alerts.state}

# A Matrix message: the headline coloured by what it says, the lines after
# it as a list (★ ones in bold), and where and when it came from.
send_matrix() {
  python3 - "$1" <<'PY'
import html, json, os, ssl, sys, time, urllib.parse, urllib.request
msg = sys.argv[1]
head, *rest = msg.split("\n")
color = {"✗": "#e5484d", "✓": "#30a46c"}.get(head[:1], "#8e4ec6")
out = ['<h4><font data-mx-color="%s" color="%s">%s</font></h4>' % (color, color, html.escape(head))]
items = []
for line in rest:
    if line[:2] in ("• ", "★ "):
        text = html.escape(line[2:])
        items.append("<li><b>★ %s</b></li>" % text if line[0] == "★" else "<li>%s</li>" % text)
    elif line.strip():
        out.append("<p>%s</p>" % html.escape(line))
if items:
    out.append("<ul>%s</ul>" % "".join(items))
out.append("<p><sub>interzone · %s · %s</sub></p>" % (html.escape(os.environ["IZ_HOST"]), time.strftime("%Y-%m-%d %H:%M %Z")))
ctx = ssl._create_unverified_context() if os.environ.get("IZ_MATRIX_INSECURE") == "true" else None
def call(method, path, body):
    req = urllib.request.Request(os.environ["IZ_MATRIX_HS"].rstrip("/") + path, json.dumps(body).encode(), method=method,
        headers={"Authorization": "Bearer " + os.environ["IZ_MATRIX_TOKEN"], "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=20, context=ctx) as resp:
        return json.load(resp)
# Joining is how an alias becomes a room ID; in a room already, it changes nothing.
room = call("POST", "/_matrix/client/v3/join/" + urllib.parse.quote(os.environ["IZ_MATRIX_ROOM"], safe=""), {})["room_id"]
call("PUT", "/_matrix/client/v3/rooms/%s/send/m.room.message/interzone%d" % (urllib.parse.quote(room, safe=""), time.time_ns()),
    {"msgtype": "m.text", "body": msg, "format": "org.matrix.custom.html", "formatted_body": "".join(out)})
PY
}

send() {
  if [ "${IZ_MATRIX:-}" = true ]; then
    send_matrix "$1"
  elif [ "$IZ_JSON" = true ]; then
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
# Mirrors only have to answer: a registry says 401, a repository root 403.
for m in ${IZ_MIRRORS:-}; do
  code=$(curl -sS -m 20 -o /dev/null -r 0-0 -w '%{http_code}' "$m" 2>/dev/null)
  if [ "${code:-000}" -lt 200 ] || [ "$code" -ge 500 ]; then
    problems+=("the mirror $m doesn't answer")
  fi
done

# Once a day, the playbook's changelog: entries newer than the titles in
# $news are sent, those about this server's components first.
news=${IZ_NEWS:-/var/lib/interzone-alerts.news}
if [ -n "${IZ_PLAYBOOK:-}" ] && [ -s "$news" ] && [ -z "$(find "$news" -mmin -1380 2>/dev/null)" ]; then
  tmp=$(mktemp -d)
  if git clone -q --depth 1 --filter=blob:none --no-checkout "$IZ_PLAYBOOK" "$tmp/p" 2>/dev/null &&
    git -C "$tmp/p" show HEAD:CHANGELOG.md > "$tmp/CHANGELOG.md" 2>/dev/null; then
    msg=$(python3 - "$news" "$tmp/CHANGELOG.md" <<'PY'
import os, re, sys
seen = set(l.strip() for l in open(sys.argv[1]))
hs = os.environ.get("IZ_HOMESERVER", "").lower()
words = [w for w in os.environ.get("IZ_WATCH", "").lower().split("|") if w]
new = []
for l in open(sys.argv[2]):
    if l.startswith("## "):
        if l[3:].strip() in seen:
            break
        new.append([l[3:].strip(), ""])
    elif new:
        new[-1][1] += l.lower()
# by title for the homeserver: nearly every entry mentions Synapse in passing
yours = lambda t, b: bool(hs and hs in t.lower()) or any(w in b or w in t.lower() for w in words)
new.sort(key=lambda n: not yours(*n))
if new:
    print("%d new in the Matrix playbook's changelog (interzone's Updates shows them):" % len(new))
for t, b in new:
    print(("★ " if yours(t, b) else "• ") + re.sub(r"\[([^]]*)\]\([^)]*\)", r"\1", t))
PY
)
    if [ -z "$msg" ] || send "$msg"; then
      sed -n 's/^## //p' "$tmp/CHANGELOG.md" > "$news"
    fi
  fi
  rm -rf "$tmp"
fi

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

    - name: Install curl, openssl, git and python3
      ansible.builtin.package:
        name: [curl, openssl, git, python3]
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
          IZ_MATRIX={{ interzone_matrix | bool | lower }}
          IZ_MATRIX_HS={{ interzone_matrix_hs | quote }}
          IZ_MATRIX_ROOM={{ interzone_matrix_room | quote }}
          IZ_MATRIX_TOKEN={{ interzone_matrix_token | quote }}
          IZ_MATRIX_INSECURE={{ interzone_matrix_insecure | bool | lower }}
          IZ_MIRRORS={{ interzone_mirrors | join(' ') | quote }}
          IZ_PLAYBOOK={{ interzone_playbook | quote }}
          IZ_HOMESERVER={{ interzone_homeserver | quote }}
          IZ_WATCH={{ interzone_watch | join('|') | quote }}
      no_log: true
      when: interzone_enabled | bool

    - name: Note the changelog entries already known
      ansible.builtin.copy:
        dest: /var/lib/interzone-alerts.news
        content: "{{ interzone_seen }}"
        force: false
        mode: "0644"
      when: interzone_enabled | bool and interzone_seen != ""

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
        - /var/lib/interzone-alerts.news
      when: not interzone_enabled | bool
`

// alertMirrors are the mirrors the server installs and updates from.
func alertMirrors(c *config.Config) []string {
	s := c.Sources
	var out []string
	if s.UseRegistry && s.RegistryPrefix != "" {
		host, _, _ := strings.Cut(s.RegistryPrefix, "/")
		out = append(out, "https://"+host+"/v2/")
	}
	for _, u := range []string{s.APTMirror, s.DockerPackageURL, s.PlaybookGit, s.RolesGitBase} {
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// watchWords tie a changelog entry to this server, as markYours does:
// the doc pages of enabled components and the words in mentions.
func watchWords(c *config.Config) []string {
	var out []string
	for _, comp := range catalog.Components {
		if c.Enabled(comp) {
			if comp.Doc != "" {
				out = append(out, strings.ToLower(comp.Doc))
			}
			out = append(out, mentions[comp.ID]...)
		}
	}
	return out
}

func hsName(id string) string {
	for _, h := range catalog.Homeservers {
		if h.ID == id {
			return h.Name
		}
	}
	return ""
}

// seenTitles are the entries of the local playbook's changelog, which the
// server starts from so only later ones are sent.
func seenTitles(c *config.Config) string {
	b, _ := os.ReadFile(filepath.Join(c.PlaybookDir, "CHANGELOG.md"))
	var out strings.Builder
	for _, l := range strings.Split(string(b), "\n") {
		if t, ok := strings.CutPrefix(l, "## "); ok {
			out.WriteString(strings.TrimSpace(t) + "\n")
		}
	}
	return out.String()
}

// AlertsProblem says what's wrong with where alerts go, or "".
func AlertsProblem(a config.Alerts) string {
	web := func(u string) bool {
		p, err := url.Parse(u)
		return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
	}
	switch {
	case !a.Matrix && !web(a.URL):
		return "The alerts address must be an http(s):// URL, e.g. https://ntfy.sh/a-long-random-topic."
	case !a.Matrix:
		return ""
	case !web(a.Homeserver):
		return "The homeserver must be an http(s):// URL, e.g. https://matrix.example.org."
	case !strings.Contains(a.Room, ":") || (a.Room[0] != '!' && a.Room[0] != '#'):
		return "The room is its ID (!abc…:example.org) or an address (#alerts:example.org)."
	case strings.TrimSpace(a.Token) == "":
		return "The access token of the account that posts the alerts is missing."
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
		if p := AlertsProblem(a); p != "" {
			return fmt.Errorf("%s", p)
		}
	}
	scheme, port := "https", "443"
	if r.cfg.SSL.Mode == "none" {
		scheme, port = "http", "80"
	}
	// A self-signed certificate is only trusted on this server's own name.
	insecure := r.cfg.SSL.Mode == "selfsigned" && strings.Contains(a.Homeserver, "//"+r.cfg.HostName())
	// The URL may hold a secret topic or token, so it goes in a file, not
	// on the command line.
	vars, _ := json.Marshal(map[string]any{"interzone_enabled": a.Enabled, "interzone_every": a.Every,
		"interzone_matrix": a.Matrix, "interzone_matrix_hs": a.Homeserver, "interzone_matrix_room": a.Room,
		"interzone_matrix_token": a.Token, "interzone_matrix_insecure": insecure,
		"interzone_url": a.URL, "interzone_json": a.JSON, "interzone_domain": r.cfg.Domain,
		"interzone_host": r.cfg.HostName(), "interzone_scheme": scheme, "interzone_port": port,
		"interzone_script": alertsScript, "interzone_mirrors": alertMirrors(r.cfg),
		"interzone_playbook": r.cfg.Sources.PlaybookGit, "interzone_homeserver": hsName(r.cfg.Homeserver),
		"interzone_watch": watchWords(r.cfg), "interzone_seen": seenTitles(r.cfg)})
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
