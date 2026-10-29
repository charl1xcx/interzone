package deploy

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"interzone/internal/config"
)

// With the Matrix Authentication Service, accounts and passwords live in
// MAS and Synapse's login and password API are off. interzone then runs
// mas-cli in the MAS container for every change, and reads the user list
// from Synapse's admin API with a compatibility token MAS issues for the
// admin account, so no password is needed to sign in.
//
// The MAS image has no shell, so a new password can only reach mas-cli as
// an argument. Those calls are no_log, which keeps the password out of
// Ansible's log on the server.

// UsesMAS reports whether the server's accounts live in MAS.
func UsesMAS(c *config.Config) bool { return c.UsesMAS() }

const masPlay = `- name: interzone mas
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: interzone mas
      ansible.builtin.command:
        argv: "{{ ['docker', 'exec', 'matrix-authentication-service', 'mas-cli', 'manage'] + interzone_args }}"
      changed_when: false
      no_log: "{{ interzone_secret }}"
`

// runMAS runs the play; tests replace it.
var runMAS = runPlayJSON

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// masRun runs mas-cli manage with args and returns what it printed.
// secret hides the arguments and the output from every log.
func masRun(ctx context.Context, c *config.Config, secret bool, args ...string) (string, error) {
	res, err := runMAS(ctx, c, "mas", masPlay, map[string]any{"interzone_args": args, "interzone_secret": secret})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "No such container"):
			return "", fmt.Errorf("start MAS under Maintenance › Services: it isn't running on the server")
		case secret:
			return "", fmt.Errorf("MAS refused %s; run 'mas-cli manage %s' on the server to see why", args[0], args[0])
		}
		return "", err
	}
	r := res["interzone mas"]
	return ansiEscape.ReplaceAllString(r.String("stdout")+"\n"+r.String("stderr"), ""), nil
}

// signupPlay makes the sign-up token in MAS. The token is the same on
// every install, so MAS refusing it as a duplicate means it's there.
const signupPlay = `- name: interzone signup
  hosts: matrix_servers
  become: true
  gather_facts: false
  tasks:
    - name: interzone signup token
      ansible.builtin.command:
        argv: [docker, exec, matrix-authentication-service, mas-cli, manage, issue-user-registration-token,
          --token, "{{ interzone_token }}", --unlimited]
      register: interzone_issued
      failed_when: interzone_issued.rc != 0 and 'user_registration_tokens_token_key' not in interzone_issued.stderr
      changed_when: interzone_issued.rc == 0
      no_log: true
`

// signupToken makes sure MAS knows the sign-up token while sign-up is
// open. A failure is a warning: the install itself worked.
func (r *Runner) signupToken(ctx context.Context) {
	if !UsesMAS(r.cfg) || !r.cfg.Signup || r.cfg.SignupToken == "" {
		return
	}
	if _, err := runMAS(ctx, r.cfg, "signup", signupPlay, map[string]any{"interzone_token": r.cfg.SignupToken}); err != nil {
		r.note(StepInstall, "! couldn't make the sign-up token in MAS; run Update again to retry")
		r.emit(Event{Step: StepInstall, Warn: true})
		return
	}
	r.note(StepInstall, "sign-up is open to anyone with the token "+r.cfg.SignupToken)
}

// localpart is the name in @name:domain.
func localpart(id string) string {
	return strings.SplitN(strings.TrimPrefix(strings.TrimSpace(id), "@"), ":", 2)[0]
}

var issuedToken = regexp.MustCompile(`Compatibility token issued: (\S+)`)

// masSession gets a Synapse admin token for login.User from MAS.
func masSession(ctx context.Context, c *config.Config, login *AdminLogin) error {
	login.Token = ""
	user := localpart(login.User)
	if user == "" {
		return ErrAdminLogin{"MAS needs the account interzone acts as"}
	}
	out, err := masRun(ctx, c, false, "issue-compatibility-token", user, "--yes-i-want-to-grant-synapse-admin-privileges")
	if m := issuedToken.FindStringSubmatch(out); err == nil && m != nil {
		login.Token = m[1]
		return nil
	}
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "not found") {
		return ErrAdminLogin{"MAS has no account " + user}
	}
	return err
}

var adminName = regexp.MustCompile(`username=(\S+)`)

// masAdmins lists the accounts MAS lets act as server admins.
func masAdmins(ctx context.Context, c *config.Config) (map[string]bool, error) {
	out, err := masRun(ctx, c, false, "list-admin-users")
	if err != nil {
		return nil, err
	}
	admins := map[string]bool{}
	for _, m := range adminName.FindAllStringSubmatch(out, -1) {
		admins[m[1]] = true
	}
	return admins, nil
}

// masUpdate makes UpdateUser's changes with mas-cli.
func masUpdate(ctx context.Context, c *config.Config, userID string, body map[string]any) error {
	user := localpart(userID)
	pw, _ := body["password"].(string)
	admin, setAdmin := body["admin"].(bool)
	switch {
	case body["deactivated"] == true:
		_, err := masRun(ctx, c, false, "lock-user", "--deactivate", user)
		return err
	case body["deactivated"] == false:
		_, err := masRun(ctx, c, false, "unlock-user", "--reactivate", user)
		return err
	case body["logout_devices"] != nil:
		if _, err := masRun(ctx, c, true, "set-password", "--ignore-complexity", user, pw); err != nil {
			return err
		}
		_, err := masRun(ctx, c, false, "kill-sessions", user)
		return err
	case pw != "":
		flag := "--no-admin"
		if admin {
			flag = "--admin"
		}
		_, err := masRun(ctx, c, true, "register-user", "--yes", "--ignore-password-complexity", "--password", pw, flag, user)
		return err
	case setAdmin:
		cmd := "demote-admin"
		if admin {
			cmd = "promote-admin"
		}
		_, err := masRun(ctx, c, false, cmd, user)
		return err
	}
	return fmt.Errorf("nothing to change")
}
