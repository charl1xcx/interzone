package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"interzone/internal/config"
)

// fakeTokens answers the tokens play with out, recording its SQL and token.
func fakeTokens(t *testing.T, out string, err error) *[][2]string {
	t.Helper()
	var calls [][2]string
	old := runMAS
	t.Cleanup(func() { runMAS = old })
	runMAS = func(_ context.Context, _ *config.Config, _, _ string, vars map[string]any, _ ...string) (map[string]TaskResult, error) {
		calls = append(calls, [2]string{vars["interzone_sql"].(string), vars["interzone_token"].(string)})
		b, _ := json.Marshal(out)
		return map[string]TaskResult{"interzone tokens": {"stdout": b}}, err
	}
	return &calls
}

func TestListTokens(t *testing.T) {
	fakeTokens(t, `[{"token":"abc","usage_limit":3,"times_used":3,"created_at":"2026-10-01T10:00:00.5+00:00","last_used_at":null,"expires_at":null,"revoked_at":null},
		{"token":"def","usage_limit":null,"times_used":0,"created_at":"2026-09-01T10:00:00+00:00","last_used_at":null,"expires_at":"2026-09-02T10:00:00+00:00","revoked_at":null}]`, nil)
	ts, err := ListTokens(context.Background(), masConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].Token != "abc" || *ts[0].UsageLimit != 3 || ts[1].UsageLimit != nil {
		t.Fatalf("%+v", ts)
	}
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if ts[0].State(now) != "used up" || ts[1].State(now) != "expired" {
		t.Errorf("states %q %q", ts[0].State(now), ts[1].State(now))
	}

	fakeTokens(t, "", errors.New("Error response from daemon: No such container: matrix-postgres"))
	if _, err := ListTokens(context.Background(), masConfig()); err == nil || !strings.Contains(err.Error(), "start Postgres") {
		t.Errorf("err %v", err)
	}
}

func TestRevokeToken(t *testing.T) {
	calls := fakeTokens(t, "abc", nil)
	if err := RevokeToken(context.Background(), masConfig(), "abc", true); err != nil {
		t.Fatal(err)
	}
	// The token goes in as a psql variable, never into the SQL.
	if sql := (*calls)[0][0]; !strings.Contains(sql, "now()") || !strings.Contains(sql, ":'tok'") || (*calls)[0][1] != "abc" {
		t.Errorf("%q", *calls)
	}
	calls = fakeTokens(t, "", nil)
	if err := RevokeToken(context.Background(), masConfig(), "zzz", false); err == nil || !strings.Contains((*calls)[0][0], "NULL") {
		t.Errorf("err %v, %q", err, *calls)
	}
}

func TestCreateToken(t *testing.T) {
	for _, tc := range []struct {
		uses    int
		expires time.Duration
		want    []string
	}{
		{0, 0, []string{"issue-user-registration-token", "--token", "abc", "--unlimited"}},
		{5, 48 * time.Hour, []string{"issue-user-registration-token", "--token", "abc", "--usage-limit", "5", "--expires-in", "172800"}},
	} {
		calls := fakeMAS(t, "")
		if err := CreateToken(context.Background(), masConfig(), "abc", tc.uses, tc.expires); err != nil {
			t.Fatal(err)
		}
		if got := (*calls)[0].args; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
