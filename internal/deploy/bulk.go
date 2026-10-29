package deploy

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"interzone/internal/config"
)

var localpartOK = regexp.MustCompile(`^[a-z0-9._=/+-]+$`)

// ReadUsersCSV reads the accounts to make, one a line: a username, then
// optionally a password and "admin". A first line naming the columns is
// skipped. Empty passwords are generated.
func ReadUsersCSV(path string) ([]NewUser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord, r.TrimLeadingSpace, r.Comment = -1, true, '#'
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var out []NewUser
	seen := map[string]bool{}
	for i, row := range rows {
		name := strings.ToLower(strings.TrimPrefix(strings.SplitN(strings.TrimSpace(row[0]), ":", 2)[0], "@"))
		if i == 0 && (name == "username" || name == "user" || name == "name") {
			continue
		}
		if name == "" {
			continue
		}
		if !localpartOK.MatchString(name) {
			return nil, fmt.Errorf("line %d: %q isn't a valid username (a–z, 0–9 and ._=-/+)", i+1, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("line %d: %s is listed twice", i+1, name)
		}
		seen[name] = true
		u := NewUser{Name: name}
		if len(row) > 1 {
			u.Password = strings.TrimSpace(row[1])
		}
		if u.Password == "" {
			u.Password = config.RandomPassword(16)
		}
		if len(row) > 2 {
			v := strings.ToLower(strings.TrimSpace(row[2]))
			u.Admin = v == "admin" || v == "yes" || v == "true" || v == "1"
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists nobody", path)
	}
	return out, nil
}

// WritePasswords saves who gets which password beside the CSV, readable
// by its owner only, and returns where.
func WritePasswords(csvPath, domain string, users []NewUser) (string, error) {
	out := strings.TrimSuffix(csvPath, filepath.Ext(csvPath)) + "-passwords.csv"
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"username", "user ID", "password", "admin"})
	for _, u := range users {
		_ = w.Write([]string{u.Name, "@" + u.Name + ":" + domain, u.Password, fmt.Sprint(u.Admin)})
	}
	w.Flush()
	return out, os.WriteFile(out, []byte(b.String()), 0o600)
}

// bulkUsers makes Options.Users with the playbook's user creator, which
// every homeserver (and MAS) supports and which leaves existing accounts
// alone. The passwords go in a file, not on the command line.
func (r *Runner) bulkUsers(ctx context.Context) error {
	var list []map[string]string
	for _, u := range r.opts.Users {
		kind := "user"
		if u.Admin {
			kind = "admin"
		}
		list = append(list, map[string]string{"username": u.Name, "initial_password": u.Password, "initial_type": kind})
	}
	vars, _ := json.Marshal(map[string]any{"matrix_user_creator_users_additional": list})
	file := filepath.Join(r.cfg.PlaybookDir, "inventory", "interzone-users-vars.json")
	if err := os.WriteFile(file, vars, 0o600); err != nil {
		return err
	}
	defer os.Remove(file)
	return r.playbookRun(ctx, StepBulkUsers, "--tags=ensure-matrix-users-created", "-e", "@"+file)
}
