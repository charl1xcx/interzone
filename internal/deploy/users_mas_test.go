package deploy

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"interzone/internal/config"
)

type masCall struct {
	args   []string
	secret bool
}

// fakeMAS records mas-cli calls and answers with out.
func fakeMAS(t *testing.T, out string) *[]masCall {
	t.Helper()
	var calls []masCall
	old := runMAS
	t.Cleanup(func() { runMAS = old })
	runMAS = func(_ context.Context, _ *config.Config, _, _ string, vars map[string]any, _ ...string) (map[string]TaskResult, error) {
		calls = append(calls, masCall{vars["interzone_args"].([]string), vars["interzone_secret"].(bool)})
		b, _ := json.Marshal(out)
		return map[string]TaskResult{"interzone mas": {"stdout": b}}, nil
	}
	return &calls
}

func masConfig() *config.Config {
	c := config.Default()
	c.Components["mas"] = true
	return c
}

func TestMASUpdate(t *testing.T) {
	for _, tc := range []struct {
		body   map[string]any
		want   [][]string
		secret bool
	}{
		{map[string]any{"deactivated": true}, [][]string{{"lock-user", "--deactivate", "bob"}}, false},
		{map[string]any{"deactivated": false}, [][]string{{"unlock-user", "--reactivate", "bob"}}, false},
		{map[string]any{"admin": true}, [][]string{{"promote-admin", "bob"}}, false},
		{map[string]any{"admin": false}, [][]string{{"demote-admin", "bob"}}, false},
		{map[string]any{"password": "pw", "logout_devices": true}, [][]string{{"set-password", "--ignore-complexity", "bob", "pw"}, {"kill-sessions", "bob"}}, true},
		{map[string]any{"password": "pw", "admin": true}, [][]string{{"register-user", "--yes", "--ignore-password-complexity", "--password", "pw", "--admin", "bob"}}, true},
	} {
		calls := fakeMAS(t, "")
		if err := UpdateUser(context.Background(), masConfig(), &AdminLogin{}, "@bob:example.org", tc.body); err != nil {
			t.Fatal(err)
		}
		var got [][]string
		for _, c := range *calls {
			got = append(got, c.args)
			// Calls that carry a password are hidden; the others aren't.
			if hasPW := strings.Contains(strings.Join(c.args, " "), "pw"); c.secret != hasPW {
				t.Errorf("%v: secret %v", c.args, c.secret)
			}
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %v", tc.body, got)
		}
	}
}

func TestMASSession(t *testing.T) {
	fakeMAS(t, "2026-10-25T21:06:04Z  INFO mas_cli::commands::manage:467 \x1b[3mcompat_access_token.id\x1b[0m=01K Compatibility token issued: mct_abc123 compat_session.id=01K")
	login := &AdminLogin{User: "@vvv:example.org"}
	if err := masSession(context.Background(), masConfig(), login); err != nil || login.Token != "mct_abc123" {
		t.Fatalf("token %q, %v", login.Token, err)
	}
	fakeMAS(t, "ERROR User not found")
	if err := masSession(context.Background(), masConfig(), login); err == nil || login.Token != "" {
		t.Fatalf("unknown user: %q, %v", login.Token, err)
	} else if _, ok := err.(ErrAdminLogin); !ok {
		t.Errorf("not a login error: %v", err)
	}
	fakeMAS(t, "INFO x user.id=01 username=vvv\nINFO x user.id=02 username=charli\n")
	got, err := masAdmins(context.Background(), masConfig())
	if err != nil || !got["vvv"] || !got["charli"] || len(got) != 2 {
		t.Errorf("admins %v %v", got, err)
	}
}
