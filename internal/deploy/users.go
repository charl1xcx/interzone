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
    return call("POST", "/_matrix/client/v3/login", {"type": "m.login.password", "identifier": {"type": "m.id.user", "user": req["admin"]}, "password": req["password"], "initial_device_display_name": "interzone"})["access_token"]
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
        return {"users": users}
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
	req["admin"], req["password"], req["token"] = login.User, login.Password, login.Token
	res, err := runPlayJSON(ctx, c, "admin", adminPlay, map[string]any{"interzone_request": req, "interzone_script": adminScript})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "No such container"):
			return nil, fmt.Errorf("Synapse isn't running on the server")
		case strings.HasPrefix(err.Error(), "interzone request:"):
			// no_log hides the reason; it can only be the file write.
			return nil, fmt.Errorf("couldn't write to /matrix on the server: is Matrix installed there, and can the SSH user become root?")
		}
		return nil, err
	}
	out := strings.TrimSpace(res["interzone admin"].String("stdout"))
	var e struct {
		Error, Errcode, Path, Token string
	}
	if json.Unmarshal([]byte(out), &e) != nil {
		return nil, fmt.Errorf("unexpected answer from the admin API: %.200s", out)
	}
	if e.Token != "" {
		login.Token = e.Token
	}
	if e.Error != "" {
		if strings.HasSuffix(e.Path, "/login") {
			login.Token = ""
			return nil, ErrAdminLogin{e.Error}
		}
		return nil, fmt.Errorf("%s", e.Error)
	}
	return json.RawMessage(out), nil
}

// ListUsers returns every account on the server, deactivated ones included.
func ListUsers(ctx context.Context, c *config.Config, login *AdminLogin) ([]MatrixUser, error) {
	out, err := adminCall(ctx, c, login, map[string]any{"op": "list"})
	if err != nil {
		return nil, err
	}
	var v struct {
		Users []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayname"`
			Admin       any    `json:"admin"`
			Deactivated any    `json:"deactivated"`
			Created     int64  `json:"creation_ts"`
			LastSeen    int64  `json:"last_seen_ts"`
		} `json:"users"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("couldn't read the user list: %w", err)
	}
	users := make([]MatrixUser, 0, len(v.Users))
	for _, u := range v.Users {
		users = append(users, MatrixUser{ID: u.Name, DisplayName: u.DisplayName, Admin: truthy(u.Admin),
			Deactivated: truthy(u.Deactivated), Created: msTime(u.Created), LastSeen: msTime(u.LastSeen)})
	}
	return users, nil
}

// UpdateUser changes an account: {"admin": true}, {"deactivated": true},
// {"password": "…", "logout_devices": true}.
func UpdateUser(ctx context.Context, c *config.Config, login *AdminLogin, userID string, body map[string]any) error {
	_, err := adminCall(ctx, c, login, map[string]any{"op": "update", "user": userID, "body": body})
	return err
}

// Logout ends the admin session, removing interzone's device.
func Logout(ctx context.Context, c *config.Config, login *AdminLogin) {
	if login.Token != "" {
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
