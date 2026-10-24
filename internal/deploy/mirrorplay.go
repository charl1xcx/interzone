package deploy

import (
	_ "embed"
	"regexp"
	"strings"

	"interzone/internal/config"
)

var (
	//go:embed plays/mirrors.yml
	mirrorsPlay []byte
	//go:embed plays/mirrors-rewrite.yml
	mirrorsRewrite []byte
	//go:embed plays/metalinks.py
	metalinksScript []byte

	playWarning = regexp.MustCompile(`"msg": "interzone-warning: (.*)"`)
)

// repoSource is one upstream repository the mirrors play can redirect.
// The play replaces Match with <mirror>/<To> in files that contain Find
// (or Match), if <mirror>/<Index> exists. Index may use the play's
// iz_release, iz_major and iz_arch.
type repoSource struct {
	Name  string `json:"name"`
	Match string `json:"match"`
	Find  string `json:"find,omitempty"`
	To    string `json:"to"`
	Index string `json:"index"`
}

// upstreams lists the repositories per Ansible os_family, with their
// upstream URL as a Python regular expression.
var upstreams = map[string][]repoSource{
	"Debian": {
		{Name: "Ubuntu", Match: `https?://(?:[a-z0-9-]+\.)*(?:archive|security)\.ubuntu\.com/ubuntu`, To: "ubuntu",
			Index: "ubuntu/dists/{{ iz_release }}/Release"},
		{Name: "Ubuntu ports", Match: `https?://(?:[a-z0-9-]+\.)*ports\.ubuntu\.com/ubuntu-ports`, To: "ubuntu-ports",
			Index: "ubuntu-ports/dists/{{ iz_release }}/Release"},
		{Name: "Debian", Match: `https?://(?:[a-z0-9-]+\.)*debian\.org/debian`, To: "debian",
			Index: "debian/dists/{{ iz_release }}/Release"},
		{Name: "Debian security", Match: `https?://(?:[a-z0-9-]+\.)*debian\.org/debian-security`, To: "debian-security",
			Index: "debian-security/dists/{{ iz_release }}-security/Release"},
		{Name: "Raspbian", Match: `https?://(?:[a-z0-9-]+\.)*raspbian\.org/raspbian`, To: "raspbian",
			Index: "raspbian/dists/{{ iz_release }}/Release"},
	},
	"RedHat": {
		{Name: "AlmaLinux", Match: `https?://repo\.almalinux\.org/almalinux`, To: "almalinux",
			Index: "almalinux/{{ iz_major }}/BaseOS/{{ iz_arch }}/os/repodata/repomd.xml"},
		{Name: "Rocky Linux", Match: `https?://dl\.rockylinux\.org/\$contentdir`, To: "rocky",
			Index: "rocky/{{ iz_major }}/BaseOS/{{ iz_arch }}/os/repodata/repomd.xml"},
		{Name: "CentOS Stream", Match: `https?://mirror\.stream\.centos\.org`, To: "centos-stream",
			Find:  `mirrors\.centos\.org/metalink\?repo=centos-|mirror\.stream\.centos\.org`,
			Index: "centos-stream/{{ iz_major }}-stream/BaseOS/{{ iz_arch }}/os/repodata/repomd.xml"},
		{Name: "Fedora", Match: `https?://download\.example/pub/fedora/linux`, To: "fedora/linux",
			Index: "fedora/linux/updates/{{ iz_major }}/Everything/{{ iz_arch }}/repodata/repomd.xml"},
		{Name: "EPEL", Match: `https?://download\.example/pub/epel`, To: "epel",
			Index: "epel/{{ iz_major }}/Everything/{{ iz_arch }}/repodata/repomd.xml"},
	},
}

// mirrorVars builds the variables for the mirrors play. Each source also
// matches the known in-country mirrors, so switching mirrors re-points
// files an earlier run rewrote.
func mirrorVars(src config.Sources) map[string]any {
	var known []string
	for _, m := range config.Mirrors(config.MirrorOS) {
		known = append(known, regexp.QuoteMeta(strings.TrimRight(m.URL, "/")))
	}
	if cur := strings.TrimRight(src.APTMirror, "/"); cur != "" {
		known = append(known, regexp.QuoteMeta(cur))
	}
	sources := map[string][]repoSource{}
	for family, list := range upstreams {
		for _, s := range list {
			alts := []string{s.Match}
			for _, k := range known {
				alts = append(alts, k+"/"+regexp.QuoteMeta(s.To))
			}
			// The lookahead stops /debian from matching /debian-security.
			s.Match = "(?:" + strings.Join(alts, "|") + `)(?=[/\s]|$)`
			if s.Find != "" {
				s.Find += "|" + s.Match
			}
			sources[family] = append(sources[family], s)
		}
	}
	return map[string]any{
		"interzone_os_mirror":     strings.TrimRight(src.APTMirror, "/"),
		"interzone_docker_mirror": strings.TrimRight(src.DockerPackageURL, "/"),
		"interzone_sources":       sources,
	}
}
