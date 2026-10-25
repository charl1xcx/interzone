package deploy

import (
	"slices"
	"testing"

	"interzone/internal/config"
)

func TestParseRef(t *testing.T) {
	for ref, want := range map[string][3]string{
		"nginx":                              {"registry-1.docker.io", "library/nginx", "latest"},
		"docker.io/matrixdotorg/synapse:v1":  {"registry-1.docker.io", "matrixdotorg/synapse", "v1"},
		"ghcr.io/a/b:1.2":                    {"ghcr.io", "a/b", "1.2"},
		"localhost:5000/x@sha256:abc":        {"localhost:5000", "x", "sha256:abc"},
		"mechatok.example/docker.io/foo/bar": {"mechatok.example", "docker.io/foo/bar", "latest"},
	} {
		h, r, tg := ParseRef(ref)
		if [3]string{h, r, tg} != want {
			t.Errorf("%s → %s %s %s, want %v", ref, h, r, tg, want)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.4.1", "v0.4.0", true},
		{"v0.4.0", "v0.4.0", false},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "dev", true},
		{"v0.3.0", "v0.4.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "aaa  interzone-linux-amd64\nbbb *interzone-linux-arm64\n"
	if checksumFor(sums, "interzone-linux-arm64") != "bbb" || checksumFor(sums, "interzone-linux-amd64") != "aaa" {
		t.Error("wrong checksum")
	}
	if checksumFor(sums, "interzone-darwin-arm64") != "" {
		t.Error("checksum for a missing file")
	}
}

func TestParseStatus(t *testing.T) {
	out := `load=0.50 0.40 0.30
cpus=4
uptime=93784.5
memtotal=4000000
memavail=1000000
disk=/ 100 40 60
disk=/matrix 100 40 60
timer=Mon 2026-10-26 03:00:00 UTC
ps={"Names":"matrix-synapse","State":"running","Status":"Up 2 hours"}
ps={"Names":"matrix-bridge","State":"exited","Status":"Exited (1)"}
stats={"Name":"matrix-synapse","CPUPerc":"1.5%","MemUsage":"300MiB / 3.8GiB","MemPerc":"7.7%"}
labels=traefik.enable=true,traefik.http.routers.matrix-client-element.rule=Host(` + "`chat.other.net`" + `),traefik.docker.network=traefik
labels=traefik.http.routers.a.rule=Host(` + "`matrix.example.org`" + `) && PathPrefix(` + "`/_matrix`" + `),traefik.http.routers.b.rule=Host(` + "`matrix.example.org`" + `)
`
	st := &ServerStatus{}
	parseStatus(st, out)
	if st.CPUs != 4 || st.Load[0] != 0.5 || st.Uptime.Hours() < 26 {
		t.Errorf("cpu/uptime: %+v", st)
	}
	if st.MemTotal != 4000000*1024 || st.MemAvail != 1000000*1024 {
		t.Errorf("memory: %d %d", st.MemTotal, st.MemAvail)
	}
	if len(st.Disks) != 1 {
		t.Errorf("same filesystem listed twice: %+v", st.Disks)
	}
	if len(st.Containers) != 2 || st.Containers[0].Name != "matrix-bridge" {
		t.Errorf("stopped containers should come first: %+v", st.Containers)
	}
	if want := []string{"chat.other.net", "matrix.example.org"}; !slices.Equal(st.Hosts, want) {
		t.Errorf("hosts %v", st.Hosts)
	}
	if st.Containers[1].CPU != "1.5%" {
		t.Errorf("stats not merged: %+v", st.Containers[1])
	}
}

func TestDiffVersions(t *testing.T) {
	ch := diffVersions(map[string]string{"a": "1", "b": "2", "c": "3"}, map[string]string{"a": "1", "b": "2.1", "d": "4"})
	got := map[string]VersionChange{}
	for _, v := range ch {
		got[v.Name] = v
	}
	if len(ch) != 3 || got["b"].New != "2.1" || got["c"].New != "" || got["d"].Old != "" {
		t.Errorf("%+v", ch)
	}
	vv := varVersions("matrix_x_version: v1.2\nmatrix_x_image: \"{{ matrix_x_version }}\"\nmatrix_y_version: \"{{ matrix_x_version }}\"\n")
	if len(vv) != 1 || vv["matrix_x_version"] != "v1.2" {
		t.Errorf("varVersions = %v", vv)
	}
}

func TestHistoryAndRollback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("INTERZONE_SERVER", "")
	c := config.Default()
	c.Domain, c.Homeserver = "example.com", "synapse"
	c.PlaybookDir = t.TempDir() // not a git repo: the subject stays empty
	c.PinnedCommit = "should-not-be-recorded"
	if err := RecordDeploy(c, ""); err == nil {
		t.Error("recorded a deploy without a commit")
	}
	if err := RecordDeploy(c, "abc123"); err != nil {
		t.Fatal(err)
	}
	h := History()
	if len(h) != 1 || h[0].Commit != "abc123" || h[0].Domain != "example.com" {
		t.Fatalf("history = %+v", h)
	}
	cur := config.Default()
	cur.PlaybookDir = "/new/place"
	cur.Target = config.Target{Mode: "ssh", Host: "new.example"}
	cur.Backups = config.BackupSchedule{Enabled: true, When: "weekly", Keep: 3}
	rc, err := RollbackConfig(cur, h[0])
	if err != nil {
		t.Fatal(err)
	}
	if rc.Domain != "example.com" || rc.PinnedCommit != "abc123" || rc.PlaybookDir != "/new/place" ||
		rc.Target.Host != "new.example" || rc.Backups.When != "weekly" {
		t.Errorf("rollback config = %+v", rc)
	}
}
