package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"interzone/internal/config"
)

// User admin talks to Synapse's admin API from inside the matrix-synapse
// container, so it needs neither the admin API exposed publicly nor DNS
// and certificates working from this computer. The request (with the admin
// password) travels in an owner-only file, never on a command line.

// MatrixUser is an account as Synapse's admin API lists it.
type MatrixUser struct {
	ID          string
	DisplayName string
	Admin       bool
	Deactivated bool
	Created     time.Time
	LastSeen    time.Time

	// Bridge is the appservice the account belongs to (a bridge's puppet
	// or bot), "" for a person. Bot marks the appservice's own account.
	Bridge string
	Bot    bool
	// Rooms and Devices are -1 when they weren't counted (an appservice
	// account, or more people than are worth asking about).
	Rooms, Devices int
	MediaCount     int
	MediaBytes     int64
	Emails         []string // and phone numbers
	Type           string   // Synapse's user_type: "", "bot" or "support"
	Locked         bool
	ShadowBanned   bool
	Avatar         bool
}

// Person reports whether the account is someone's, not a bridge's.
func (u MatrixUser) Person() bool { return u.Bridge == "" }

// UserList is everyone on the server, with a few totals.
type UserList struct {
	Users []MatrixUser
	Rooms int // rooms the server knows of, -1 if it didn't say
}

// AdminLogin is the account interzone signs in as to administer users.
type AdminLogin struct {
	User     string
	Password string
	// Token is the session's access token, reused between calls so
	// Synapse's login rate limit isn't hit. Set by every call.
	Token string
}

// ErrAdminLogin means the admin username or password was refused.
type ErrAdminLogin struct{ Msg string }

func (e ErrAdminLogin) Error() string { return "the admin login was refused: " + e.Msg }

const adminScript = `
import json, sys, urllib.request, urllib.parse, urllib.error
req = json.load(sys.stdin)
base = "http://127.0.0.1:8008"
class Failed(Exception):
    pass
def call(method, path, body=None, token=None):
    data = None if body is None else json.dumps(body).encode()
    r = urllib.request.Request(base + path, data=data, method=method, headers={"Content-Type": "application/json"})
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r, timeout=60) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        try:
            err = json.load(e)
        except Exception:
            err = {}
        raise Failed({"error": err.get("error") or ("HTTP %d" % e.code), "errcode": err.get("errcode", ""), "path": path.split("?")[0]})
def login():
    if req.get("mas"):
        raise Failed({"error": "the session was refused", "errcode": "M_UNKNOWN_TOKEN", "path": "/token"})
    return call("POST", "/_matrix/client/v3/login", {"type": "m.login.password", "identifier": {"type": "m.id.user", "user": req["admin"]}, "password": req["password"], "initial_device_display_name": "interzone"})["access_token"]
def total(path, user, tok):
    try:
        return call("GET", path % urllib.parse.quote(user), token=tok).get("total", -1)
    except Failed:
        return -1
def appservices():
    # The bridges' registrations name the accounts they own.
    out = []
    try:
        import re, yaml
        cfg = yaml.safe_load(open("/data/homeserver.yaml"))
        for path in cfg.get("app_service_config_files") or []:
            try:
                reg = yaml.safe_load(open(path))
                regexes = [re.compile(n["regex"]) for n in (reg.get("namespaces") or {}).get("users") or [] if n.get("regex")]
                out.append((str(reg.get("id", path)), str(reg.get("sender_localpart", "")), regexes))
            except Exception:
                pass
    except Exception:
        pass
    return out
def run(tok):
    if req["op"] == "logout":
        call("POST", "/_matrix/client/v3/logout", {}, token=tok)
        return {"ok": True}
    if req["op"] == "list":
        users, frm = [], "0"
        while frm is not None:
            page = call("GET", "/_synapse/admin/v2/users?guests=false&deactivated=true&limit=500&from=" + urllib.parse.quote(str(frm)), token=tok)
            users += page.get("users", [])
            frm = page.get("next_token")
        services = appservices()
        people = 0
        for u in users:
            u["appservice"], u["bot"] = "", False
            local = u["name"][1:].split(":", 1)[0]
            for sid, sender, regexes in services:
                if local == sender or any(r.match(u["name"]) for r in regexes):
                    u["appservice"], u["bot"] = sid, local == sender
                    break
            if u["appservice"] or people >= 300:
                continue
            people += 1
            u["rooms"] = total("/_synapse/admin/v1/users/%s/joined_rooms", u["name"], tok)
            u["devices"] = total("/_synapse/admin/v2/users/%s/devices", u["name"], tok)
            try:
                d = call("GET", "/_synapse/admin/v2/users/" + urllib.parse.quote(u["name"]), token=tok)
                u["threepids"] = [t.get("address", "") for t in d.get("threepids") or []]
            except Failed:
                pass
        media, frm = {}, "0"
        try:
            while frm is not None:
                page = call("GET", "/_synapse/admin/v1/statistics/users/media?limit=500&from=" + urllib.parse.quote(str(frm)), token=tok)
                for m in page.get("users", []):
                    media[m["user_id"]] = [m.get("media_count", 0), m.get("media_length", 0)]
                frm = page.get("next_token")
        except Failed:
            pass
        for u in users:
            u["media"] = media.get(u["name"], [0, 0])
        rooms = -1
        try:
            rooms = call("GET", "/_synapse/admin/v1/rooms?limit=1", token=tok).get("total_rooms", -1)
        except Failed:
            pass
        return {"users": users, "rooms": rooms}
    if req["op"] == "logout_all":
        me = call("GET", "/_matrix/client/v3/account/whoami", token=tok).get("device_id")
        path = "/_synapse/admin/v2/users/" + urllib.parse.quote(req["user"])
        devices = [d["device_id"] for d in call("GET", path + "/devices", token=tok).get("devices", []) if d["device_id"] != me]
        if devices:
            call("POST", path + "/delete_devices", {"devices": devices}, token=tok)
        return {"ok": True, "count": len(devices)}
    call("PUT", "/_synapse/admin/v2/users/" + urllib.parse.quote(req["user"]), req["body"], token=tok)
    return {"ok": True}
try:
    tok = req.get("token") or login()
    try:
        out = run(tok)
    except Failed as e:
        if e.args[0]["errcode"] != "M_UNKNOWN_TOKEN" or not req.get("token") or req["op"] == "logout":
            raise
        tok = login()
        out = run(tok)
    out["token"] = tok
    print(json.dumps(out))
except Failed as e:
    print(json.dumps(e.args[0]))
`

const adminPlay = `- name: interzone admin
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - block:
        - name: interzone request
          ansible.builtin.copy:
            content: "{{ interzone_request | to_json }}"
            dest: /matrix/.interzone-admin-request.json
            mode: "0600"
          no_log: true

        - name: interzone admin
          ansible.builtin.shell: docker exec -i matrix-synapse python3 -c "$IZ_SCRIPT" < /matrix/.interzone-admin-request.json
          environment:
            IZ_SCRIPT: "{{ interzone_script }}"
          changed_when: false
      always:
        - name: interzone cleanup
          ansible.builtin.file:
            path: /matrix/.interzone-admin-request.json
            state: absent
`

func adminCall(ctx context.Context, c *config.Config, login *AdminLogin, req map[string]any) (json.RawMessage, error) {
	if c.Homeserver != "synapse" {
		return nil, fmt.Errorf("user admin needs Synapse's admin API")
	}
	mas := UsesMAS(c)
	if mas && login.Token == "" {
		if err := masSession(ctx, c, login); err != nil {
			return nil, err
		}
	}
	out, e, err := adminRun(ctx, c, login, req, mas)
	if err == nil && mas && e.Errcode == "M_UNKNOWN_TOKEN" {
		// The session ended (MAS was reset, or the user signed it out): get a new one.
		if err := masSession(ctx, c, login); err != nil {
			return nil, err
		}
		out, e, err = adminRun(ctx, c, login, req, mas)
	}
	if err != nil {
		return nil, err
	}
	if e.Error != "" {
		if strings.HasSuffix(e.Path, "/login") || e.Path == "/token" {
			login.Token = ""
			return nil, ErrAdminLogin{e.Error}
		}
		return nil, fmt.Errorf("%s", e.Error)
	}
	return out, nil
}

type adminError struct{ Error, Errcode, Path, Token string }

func adminRun(ctx context.Context, c *config.Config, login *AdminLogin, req map[string]any, mas bool) (json.RawMessage, adminError, error) {
	var e adminError
	req["admin"], req["password"], req["token"], req["mas"] = login.User, login.Password, login.Token, mas
	res, err := runPlayJSON(ctx, c, "admin", adminPlay, map[string]any{"interzone_request": req, "interzone_script": adminScript})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "No such container"):
			return nil, e, fmt.Errorf("Synapse isn't running on the server")
		case strings.HasPrefix(err.Error(), "interzone request:"):
			// no_log hides the reason; it can only be the file write.
			return nil, e, fmt.Errorf("couldn't write to /matrix on the server: is Matrix installed there, and can the SSH user become root?")
		}
		return nil, e, err
	}
	out := strings.TrimSpace(res["interzone admin"].String("stdout"))
	if json.Unmarshal([]byte(out), &e) != nil {
		return nil, e, fmt.Errorf("unexpected answer from the admin API: %.200s", out)
	}
	if e.Token != "" {
		login.Token = e.Token
	}
	return json.RawMessage(out), e, nil
}

// ListUsers returns every account on the server, deactivated ones
// included, with what they use: rooms, sessions and media.
func ListUsers(ctx context.Context, c *config.Config, login *AdminLogin) (*UserList, error) {
	out, err := adminCall(ctx, c, login, map[string]any{"op": "list"})
	if err != nil {
		return nil, err
	}
	var admins map[string]bool
	if UsesMAS(c) {
		// MAS decides who may act as a server admin, not Synapse.
		if admins, err = masAdmins(ctx, c); err != nil {
			return nil, err
		}
	}
	return parseUsers(out, admins)
}

// parseUsers reads the admin script's list; admins, if set, overrides who
// is an admin.
func parseUsers(out []byte, admins map[string]bool) (*UserList, error) {
	var v struct {
		Users []struct {
			Name         string   `json:"name"`
			DisplayName  string   `json:"displayname"`
			Admin        any      `json:"admin"`
			Deactivated  any      `json:"deactivated"`
			Created      int64    `json:"creation_ts"`
			LastSeen     int64    `json:"last_seen_ts"`
			Appservice   string   `json:"appservice"`
			Bot          bool     `json:"bot"`
			Rooms        *int     `json:"rooms"`
			Devices      *int     `json:"devices"`
			Media        []int64  `json:"media"`
			Threepids    []string `json:"threepids"`
			UserType     string   `json:"user_type"`
			Locked       any      `json:"locked"`
			ShadowBanned any      `json:"shadow_banned"`
			Avatar       string   `json:"avatar_url"`
		} `json:"users"`
		Rooms *int `json:"rooms"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("couldn't read the user list: %w", err)
	}
	list := &UserList{Users: make([]MatrixUser, 0, len(v.Users)), Rooms: -1}
	if v.Rooms != nil {
		list.Rooms = *v.Rooms
	}
	count := func(n *int) int {
		if n == nil {
			return -1
		}
		return *n
	}
	for _, u := range v.Users {
		admin := truthy(u.Admin)
		if admins != nil {
			admin = admins[localpart(u.Name)]
		}
		mu := MatrixUser{ID: u.Name, DisplayName: u.DisplayName, Admin: admin,
			Deactivated: truthy(u.Deactivated), Created: msTime(u.Created), LastSeen: msTime(u.LastSeen),
			Bridge: u.Appservice, Bot: u.Bot, Rooms: count(u.Rooms), Devices: count(u.Devices),
			Emails: u.Threepids, Type: u.UserType, Locked: truthy(u.Locked), ShadowBanned: truthy(u.ShadowBanned), Avatar: u.Avatar != ""}
		if len(u.Media) == 2 {
			mu.MediaCount, mu.MediaBytes = int(u.Media[0]), u.Media[1]
		}
		list.Users = append(list.Users, mu)
	}
	return list, nil
}

// SignOutEverywhere ends all of a user's sessions, leaving the password
// alone. interzone's own session stays when it's the admin's.
func SignOutEverywhere(ctx context.Context, c *config.Config, login *AdminLogin, userID string) error {
	if UsesMAS(c) {
		_, err := masRun(ctx, c, false, "kill-sessions", localpart(userID))
		return err
	}
	_, err := adminCall(ctx, c, login, map[string]any{"op": "logout_all", "user": userID})
	return err
}

// UpdateUser changes an account: {"admin": true}, {"deactivated": true},
// {"password": "…", "logout_devices": true}.
func UpdateUser(ctx context.Context, c *config.Config, login *AdminLogin, userID string, body map[string]any) error {
	if UsesMAS(c) {
		return masUpdate(ctx, c, userID, body)
	}
	_, err := adminCall(ctx, c, login, map[string]any{"op": "update", "user": userID, "body": body})
	return err
}

// Logout ends the admin session, removing interzone's device.
func Logout(ctx context.Context, c *config.Config, login *AdminLogin) {
	if login.Token != "" && !UsesMAS(c) {
		_, _ = adminCall(ctx, c, login, map[string]any{"op": "logout"})
		login.Token = ""
	}
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	}
	return false
}

// msTime converts a Synapse timestamp, which is in seconds for creation_ts
// and milliseconds for last_seen_ts.
func msTime(n int64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n < 1e11:
		return time.Unix(n, 0)
	}
	return time.UnixMilli(n)
}
