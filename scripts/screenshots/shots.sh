#!/bin/bash
# shots.sh: retake the README's screenshots (docs/screenshots).
#
#   scripts/screenshots/shots.sh all                 retake every screenshot, start to finish
#
# or one at a time:
#
#   scripts/screenshots/shots.sh start [COLSxROWS]   build interzone and run it on a demo
#                                                    server in tmux session "shot" (120x36)
#   scripts/screenshots/shots.sh grab NAME [WIDTH]   save the session's screen as NAME.png
#                                                    (1400 px wide; phone shots: WIDTH 0)
#   scripts/screenshots/shots.sh phone A B C         join three phone shots into phone.png
#   scripts/screenshots/shots.sh users               draw users.png, dashboard.png and host-map.png
#                                                    from the layout tests' sample data (no server needed)
#   scripts/screenshots/shots.sh stop
#
# Drive the session between grabs with tmux send-keys or tmux attach -t shot.
# The demo lives in a temporary directory with its own config, and its
# target is 192.0.2.1 (an address reserved for examples), so nothing it does
# reaches a real server, even screens that ask the server as they open.
# Fine-tune lists the roles of your playbook copy (INTERZONE_PLAYBOOK, by
# default interzone's own), linked into the demo read-only.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=${SHOTS_DIR:-${TMPDIR:-/tmp}/interzone-shots}
out=$repo/docs/screenshots
playbook=${INTERZONE_PLAYBOOK:-${XDG_DATA_HOME:-$HOME/.local/share}/interzone/matrix-docker-ansible-deploy}

start() {
	size=${1:-120x36}
	rm -rf "$work" && mkdir -p "$work/raw"
	(cd "$repo" && go build -o "$work/iz" .)
	# A relative XDG_CONFIG_HOME makes interzone keep its config in ./.interzone,
	# and the playbook path below stays as "~/…" on screen.
	pb="$work/~/.local/share/interzone/playbook"
	mkdir -p "$work/.interzone" "$pb/inventory/host_vars/matrix.example.org"
	touch "$pb/inventory/host_vars/matrix.example.org/vars.yml"
	if [ -d "$playbook/roles" ]; then
		for e in "$playbook"/*; do
			[ "$(basename "$e")" = inventory ] || ln -s "$e" "$pb/"
		done
	fi
	cat >"$work/.interzone/config.json" <<'JSON'
{
  "Domain": "example.org",
  "Homeserver": "synapse",
  "Target": {"Mode": "ssh", "Host": "192.0.2.1", "User": "demo"},
  "PlaybookDir": "~/.local/share/interzone/playbook",
  "SSL": {"Mode": "selfsigned"},
  "Sources": {"Blackout": true},
  "Components": {"element": true, "element-admin": true, "cinny": true, "coturn": true, "exim": true,
    "double-puppet": true, "discord": true, "signal": true, "telegram": true, "whatsapp": true}
}
JSON
	tmux kill-session -t shot 2>/dev/null || true
	tmux new-session -d -s shot -x "${size%x*}" -y "${size#*x}" -c "$work" \
		"env -u XDG_CONFIG_HOME XDG_CONFIG_HOME=rel XDG_DATA_HOME='~/.local/share' INTERZONE_HEALTH=off INTERZONE_MOTION=off COLORTERM=truecolor ./iz 2>iz.err"
	echo "running in tmux session shot ($size); attach with: tmux attach -t shot"
}

grab() {
	name=$1 width=${2:-1400}
	tmux capture-pane -t shot -e -p >"$work/raw/$name.ans"
	if [ "$width" = 0 ]; then
		python3 "$repo/scripts/screenshots/render.py" "$work/raw/$name.ans" "$work/raw/$name.png" >/dev/null
	else
		python3 "$repo/scripts/screenshots/render.py" "$work/raw/$name.ans" "$out/$name.png" "$width"
	fi
}

phone() {
	pngs=()
	for n in "$@"; do pngs+=("$work/raw/$n.png"); done
	python3 "$repo/scripts/screenshots/render.py" --join "$out/phone.png" "${pngs[@]}"
}

users() {
	mkdir -p "$work/raw"
	(cd "$repo" && INTERZONE_SHOT="$work/raw" go test ./internal/ui -run '^TestShot$' -count=1 >/dev/null)
	for n in users dashboard host-map; do
		python3 "$repo/scripts/screenshots/render.py" "$work/raw/$n.ans" "$out/$n.png" 1400
	done
}

stop() {
	tmux kill-session -t shot 2>/dev/null || true
}

# ── Driving the demo, for all ───────────────────────────────────────

screen() { tmux capture-pane -t shot -p; }

# Not "screen | grep -q": grep stops at the first match and, under
# pipefail, tmux writing the rest can make it look like no match.
shows() { [[ $(screen) == *"$1"* ]]; }

# waitfor waits up to 10 s for text to appear on the screen.
waitfor() {
	for _ in $(seq 100); do
		shows "$1" && return
		sleep 0.1
	done
	last=$(screen)
	stop
	echo "shots.sh: gave up waiting for \"$1\"; the screen was:" >&2
	echo "$last" >&2
	exit 1
}

# press sends one key and waits for the screen to change. It never sends
# enter on Manage, where enter runs the action under the cursor.
press() {
	if [ "$1" = Enter ] && shows "Keep it running"; then
		stop
		echo "shots.sh: refusing to press enter on Manage" >&2
		exit 1
	fi
	before=$(screen)
	tmux send-keys -t shot "$1"
	for _ in $(seq 20); do
		[ "$(screen)" != "$before" ] && return
		sleep 0.1
	done
}

# toHomeserver goes from Welcome through the Server form to the Homeserver
# step, with the cursor on the Bridge hub preset.
toHomeserver() {
	waitfor "Change the setup"
	press Down
	waitfor "◉  Change the setup"
	press Enter
	waitfor "Tell me about your server"
	[ "${1:-}" = server ] && grab server
	for _ in $(seq 40); do # enter moves field by field
		shows "Choose your homeserver" && break
		press Enter
	done
	waitfor "Choose your homeserver"
	press Up && press Up && press Up
	waitfor "› Bridge hub"
}

all() {
	start 120x36
	waitfor "Keep it running"
	sleep 0.5 # the first keys after start can get lost
	grab manage
	press Escape
	waitfor "Manage the server"
	grab welcome
	toHomeserver server
	grab homeserver
	press Down && press Down && press Down
	press Enter
	waitfor "Pick what you want"
	grab components
	press Enter
	waitfor "Fill in the details"
	grab settings
	for _ in $(seq 8); do
		shows "Manage the server" && break
		press Escape
	done
	press Up
	waitfor "◉  Manage the server"
	press Enter
	waitfor "Keep it running"
	for _ in $(seq 5); do
		shows "Fine-tune the playbook's roles" && break
		press f
	done
	waitfor "Fine-tune the playbook's roles"
	grab tuning
	stop
	users

	start 46x40
	waitfor "Keep it running"
	sleep 0.5
	grab p-manage 0
	press C-b
	waitfor "✕ close"
	grab p-menu 0
	press Escape
	waitfor "Keep it running"
	press Escape
	toHomeserver
	grab p-presets 0
	stop
	phone p-manage p-menu p-presets
}

cmd=${1:-}
case $cmd in
start | grab | phone | users | stop | all)
	shift
	"$cmd" "$@"
	;;
*)
	sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
	exit 1
	;;
esac
