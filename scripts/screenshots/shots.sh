#!/bin/bash
# shots.sh: retake the README's screenshots (docs/screenshots).
#
#   scripts/screenshots/shots.sh start [COLSxROWS]   build interzone and run it on a demo
#                                                    server in tmux session "shot" (120x36)
#   scripts/screenshots/shots.sh grab NAME [WIDTH]   save the session's screen as NAME.png
#                                                    (1400 px wide; phone shots: WIDTH 0)
#   scripts/screenshots/shots.sh phone A B C         join three phone shots into phone.png
#   scripts/screenshots/shots.sh users               draw users.png from the layout tests'
#                                                    sample people (no server needed)
#   scripts/screenshots/shots.sh stop
#
# Drive the session between grabs with tmux send-keys or tmux attach -t shot.
# The demo lives in a temporary directory with its own config, and its
# target is 192.0.2.1 (an address reserved for examples), so nothing it does
# reaches a real server, even screens that ask the server as they open.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=${SHOTS_DIR:-${TMPDIR:-/tmp}/interzone-shots}
out=$repo/docs/screenshots

case ${1:-} in
start)
	size=${2:-120x36}
	rm -rf "$work" && mkdir -p "$work/raw"
	(cd "$repo" && go build -o "$work/iz" .)
	# A relative XDG_CONFIG_HOME makes interzone keep its config in ./.interzone,
	# and the playbook path below stays as "~/…" on screen.
	mkdir -p "$work/.interzone" "$work/~/.local/share/interzone/playbook/inventory/host_vars/matrix.example.org"
	touch "$work/~/.local/share/interzone/playbook/inventory/host_vars/matrix.example.org/vars.yml"
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
		"env -u XDG_CONFIG_HOME XDG_CONFIG_HOME=rel XDG_DATA_HOME='~/.local/share' INTERZONE_HEALTH=off COLORTERM=truecolor ./iz 2>iz.err"
	echo "running in tmux session shot ($size); attach with: tmux attach -t shot"
	;;
grab)
	name=$2 width=${3:-1400}
	tmux capture-pane -t shot -e -p >"$work/raw/$name.ans"
	if [ "$width" = 0 ]; then
		python3 "$repo/scripts/screenshots/render.py" "$work/raw/$name.ans" "$work/raw/$name.png"
	else
		python3 "$repo/scripts/screenshots/render.py" "$work/raw/$name.ans" "$out/$name.png" "$width"
	fi
	;;
phone)
	shift
	pngs=()
	for n in "$@"; do pngs+=("$work/raw/$n.png"); done
	python3 "$repo/scripts/screenshots/render.py" --join "$out/phone.png" "${pngs[@]}"
	;;
users)
	mkdir -p "$work/raw"
	(cd "$repo" && INTERZONE_SHOT="$work/raw" go test ./internal/ui -run '^TestShot$' -count=1 >/dev/null)
	python3 "$repo/scripts/screenshots/render.py" "$work/raw/users.ans" "$out/users.png" 1400
	;;
stop)
	tmux kill-session -t shot 2>/dev/null || true
	;;
*)
	sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 1
	;;
esac
