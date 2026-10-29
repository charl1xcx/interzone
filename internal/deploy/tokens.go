package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"interzone/internal/config"
)

// The registration tokens people sign up to MAS with. mas-cli can only
// make them, so they're read and revoked in MAS's table in the bundled
// Postgres, the way MAS itself does it: revoking sets revoked_at, and
// clearing it brings a token back.

// RegToken is a registration token and how it has been used.
type RegToken struct {
	Token      string     `json:"token"`
	UsageLimit *int       `json:"usage_limit"` // nil: unlimited
	TimesUsed  int        `json:"times_used"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// State is "active", or why the token no longer works.
func (t RegToken) State(now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
		return "expired"
	case t.UsageLimit != nil && t.TimesUsed >= *t.UsageLimit:
		return "used up"
	}
	return "active"
}

// tokensPlay runs SQL from stdin against MAS's database, with the token
// as a psql variable so it's quoted by psql.
const tokensPlay = `- name: interzone tokens
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: interzone tokens
      ansible.builtin.command:
        argv: [docker, exec, -i, --env-file=/matrix/postgres/env-postgres-psql, matrix-postgres,
          psql, -h, matrix-postgres, -d, matrix_authentication_service, -X, -q, -A, -t,
          -v, ON_ERROR_STOP=1, -v, "tok={{ interzone_token }}"]
        stdin: "{{ interzone_sql }}"
      changed_when: false
`

const listTokensSQL = `SELECT coalesce(json_agg(json_build_object('token', token, 'usage_limit', usage_limit,
  'times_used', times_used, 'created_at', created_at, 'last_used_at', last_used_at,
  'expires_at', expires_at, 'revoked_at', revoked_at) ORDER BY created_at DESC), '[]')
FROM user_registration_tokens;`

func tokensSQL(ctx context.Context, c *config.Config, sql, token string) (string, error) {
	res, err := runMAS(ctx, c, "tokens", tokensPlay, map[string]any{"interzone_sql": sql, "interzone_token": token})
	if err != nil {
		if strings.Contains(err.Error(), "No such container") {
			return "", fmt.Errorf("start Postgres under Maintenance › Services: it isn't running, so MAS's tokens can't be read")
		}
		return "", fmt.Errorf("couldn't reach MAS's database: %w", err)
	}
	return strings.TrimSpace(res["interzone tokens"].String("stdout")), nil
}

// ListTokens returns every registration token MAS has, newest first.
func ListTokens(ctx context.Context, c *config.Config) ([]RegToken, error) {
	out, err := tokensSQL(ctx, c, listTokensSQL, "")
	if err != nil {
		return nil, err
	}
	var ts []RegToken
	if err := json.Unmarshal([]byte(out), &ts); err != nil {
		return nil, fmt.Errorf("couldn't read MAS's tokens: %v", err)
	}
	return ts, nil
}

// CreateToken makes a token in MAS. uses 0 is unlimited and expires 0
// never runs out.
func CreateToken(ctx context.Context, c *config.Config, token string, uses int, expires time.Duration) error {
	args := []string{"issue-user-registration-token", "--token", token}
	if uses > 0 {
		args = append(args, "--usage-limit", strconv.Itoa(uses))
	} else {
		args = append(args, "--unlimited")
	}
	if expires > 0 {
		args = append(args, "--expires-in", strconv.Itoa(int(expires.Seconds())))
	}
	if _, err := masRun(ctx, c, false, args...); err != nil {
		if strings.Contains(err.Error(), "user_registration_tokens_token_key") {
			return fmt.Errorf("there's already a token %s", token)
		}
		return err
	}
	return nil
}

// RevokeToken stops a token from working, or with revoke false lets it
// work again.
func RevokeToken(ctx context.Context, c *config.Config, token string, revoke bool) error {
	set := "now()"
	if !revoke {
		set = "NULL"
	}
	out, err := tokensSQL(ctx, c, "UPDATE user_registration_tokens SET revoked_at = "+set+
		" WHERE token = :'tok' RETURNING token;", token)
	if err != nil {
		return err
	}
	if out == "" {
		return fmt.Errorf("MAS has no token %s", token)
	}
	return nil
}
