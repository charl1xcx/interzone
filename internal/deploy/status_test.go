package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestStatusHistory(t *testing.T) {
	day := int64(24 * 3600)
	now := time.Now().Unix()
	var out strings.Builder
	// 10 GB disk, 1 GB more used each day, 8 days: 2 GB left, full in 2 days.
	for i := int64(0); i <= 8; i++ {
		used := i << 20
		out.WriteString("hist=" + strings.Join([]string{
			itoa(now - (8-i)*day), "1.00", "2", "1048576", "4194304", itoa(used), itoa(10 << 20)}, " ") + "\n")
	}
	st := &ServerStatus{}
	parseStatus(st, out.String())
	if len(st.History) != 9 || st.History[0].Load != 0.5 || st.History[8].DiskUsed != 8<<30 || st.History[0].MemTotal != 4<<30 {
		t.Fatalf("%+v", st.History)
	}
	if eta := time.Until(st.DiskFull()); eta < 47*time.Hour || eta > 49*time.Hour {
		t.Errorf("full in %v, want 2 days", eta)
	}
	st.History = st.History[:1]
	if !st.DiskFull().IsZero() {
		t.Error("an estimate from one sample")
	}
}

// The stats script the status play installs runs and writes a sample.
func TestStatsScript(t *testing.T) {
	var play []struct {
		Tasks []struct {
			Shell struct {
				Cmd string `yaml:"cmd"`
			} `yaml:"ansible.builtin.shell"`
		}
	}
	if err := yaml.Unmarshal([]byte(statusPlay), &play); err != nil {
		t.Fatal(err)
	}
	sh := play[0].Tasks[0].Shell.Cmd
	if err := exec.Command("bash", "-n", "-c", sh).Run(); err != nil {
		t.Fatal("the status shell doesn't parse:", err)
	}
	_, rest, _ := strings.Cut(sh, "<<'EOF'\n")
	script, _, ok := strings.Cut(rest, "\nEOF\n")
	if !ok {
		t.Fatal("no stats script in the status play")
	}
	dir := t.TempDir()
	script = strings.ReplaceAll(script, "/var/lib/interzone-stats", filepath.Join(dir, "stats"))
	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "stats"))
	if f := strings.Fields(string(b)); len(f) != 7 {
		t.Errorf("sample %q", b)
	}
}
