package config

import "slices"

// MirrorKind says what a mirror serves.
type MirrorKind string

const (
	MirrorOS     MirrorKind = "os"     // distro packages: /ubuntu, /debian, /almalinux, /archlinux…
	MirrorDocker MirrorKind = "docker" // a copy of download.docker.com/linux
	MirrorPyPI   MirrorKind = "pypi"   // a PyPI simple index
)

// Distros, as named in Mirror.Covers. OS mirrors carry them at the usual
// paths (/ubuntu, /ubuntu-ports, /debian and /debian-security, /raspbian,
// /almalinux, /rocky, /centos-stream, /fedora/linux, /epel, /archlinux).
// Docker mirrors use download.docker.com's names: debian, ubuntu, raspbian,
// centos (also used for AlmaLinux and Rocky), rhel and fedora.
const (
	Ubuntu       = "ubuntu"
	UbuntuPorts  = "ubuntu-ports" // Ubuntu on ARM and other non-x86 CPUs
	Debian       = "debian"
	Raspbian     = "raspbian"
	AlmaLinux    = "almalinux"
	Rocky        = "rocky"
	CentOSStream = "centos-stream"
	Fedora       = "fedora"
	EPEL         = "epel"
	Arch         = "archlinux"
	CentOS       = "centos"
	RHEL         = "rhel"
)

// DistroNames are the display names of the distros above.
var DistroNames = map[string]string{
	Ubuntu: "Ubuntu", UbuntuPorts: "Ubuntu ARM", Debian: "Debian", Raspbian: "Raspbian",
	AlmaLinux: "AlmaLinux", Rocky: "Rocky Linux", CentOSStream: "CentOS Stream",
	Fedora: "Fedora", EPEL: "EPEL", Arch: "Arch Linux", CentOS: "CentOS/Alma/Rocky", RHEL: "RHEL",
}

type Mirror struct {
	Kind MirrorKind
	Name string
	URL  string
	// Covers lists the distros the mirror carries fresh copies of. PyPI
	// mirrors leave it empty.
	Covers []string
}

// SelfHostedMirror is an interzone-mirror
// (https://itsjustadream.g0th.ir/vvvomit/interzone-mirror): one host that
// carries exactly what interzone and the playbook install on Debian, Ubuntu
// (x86 and ARM), Raspbian and Arch, plus Docker's packages for the APT
// distros and a PyPI index with Ansible. It is the default for all three
// kinds; RHEL-family and Fedora servers need one of the public mirrors.
const SelfHostedMirror = "https://infamous.g0th.ir"

// KnownMirrors lists public mirrors hosted in Iran, best first. Each one
// was checked in October 2026 to be in Iran and to serve fresh packages
// for the distros it covers; `interzone mirrors` and the network tests in
// internal/deploy re-check them.
//
// Apart from interzone-mirror, no mirror in Iran was found for Ubuntu ARM
// (ubuntu-ports), Raspbian, Rocky Linux, CentOS Stream or Fedora. Runflare's Linux mirror
// (mirror-linux.runflare.com) carried Ubuntu, Debian, AlmaLinux, EPEL and
// Arch but started answering "402 Insufficient credit" in October 2026.
var KnownMirrors = []Mirror{
	{MirrorOS, "interzone-mirror", SelfHostedMirror, []string{Ubuntu, UbuntuPorts, Debian, Raspbian, Arch}},
	{MirrorDocker, "interzone-mirror", SelfHostedMirror + "/docker", []string{Debian, Ubuntu, Raspbian}},
	{MirrorPyPI, "interzone-mirror", SelfHostedMirror + "/pypi/simple", nil},

	{MirrorOS, "Hostbaran", "https://mirror.hostbaran.com", []string{Ubuntu, Debian, AlmaLinux, EPEL, Arch}},
	{MirrorOS, "Abrha", "https://repo.abrha.net", []string{Ubuntu, Debian, AlmaLinux, EPEL}},
	{MirrorOS, "ArvanCloud", "https://mirror.arvancloud.ir", []string{Ubuntu, Debian, Arch}},
	{MirrorOS, "IranServer", "https://mirror.iranserver.com", []string{Ubuntu, Debian, AlmaLinux}},
	{MirrorOS, "MobinHost", "https://mirror.mobinhost.com", []string{Ubuntu, Debian, EPEL, Arch}},
	{MirrorOS, "FamaServer", "https://mirror.famaserver.com", []string{Ubuntu, AlmaLinux, EPEL, Arch}},

	{MirrorDocker, "CDN.ir", "https://mirror.cdn.ir/repository/docker/linux", []string{Debian, Ubuntu, Raspbian, CentOS, RHEL, Fedora}},
	{MirrorDocker, "Abrha", "https://repo.abrha.net/docker", []string{Debian, Ubuntu}},

	{MirrorPyPI, "Liara", "https://package-mirror.liara.ir/repository/pypi/simple", nil},
	{MirrorPyPI, "IranServer", "https://pypi.iranserver.com/repository/pypi/simple", nil},
	{MirrorPyPI, "Runflare", "https://mirror-pypi.runflare.com/simple", nil},
	{MirrorPyPI, "Abrha", "https://mirror.abrha.net/repository/pypi/simple", nil},
	{MirrorPyPI, "Chabokan", "https://mirror2.chabokan.net/pypi/simple", nil},
}

// KnownRegistries are container registries that carry every registry's
// images under one prefix (<prefix><owner>/<image>), best first. Blackout
// deploys fall back to them when the chosen registry lacks an image.
var KnownRegistries = []string{DefaultRegistryPrefix}

// Mirrors returns the known mirrors of one kind, best first.
func Mirrors(kind MirrorKind) []Mirror {
	var out []Mirror
	for _, m := range KnownMirrors {
		if m.Kind == kind {
			out = append(out, m)
		}
	}
	return out
}

// MirrorsFor returns the known mirrors of kind that carry distro.
func MirrorsFor(kind MirrorKind, distro string) []Mirror {
	var out []Mirror
	for _, m := range Mirrors(kind) {
		if slices.Contains(m.Covers, distro) {
			out = append(out, m)
		}
	}
	return out
}

// DefaultMirror is the URL blackout mode starts with for kind.
func DefaultMirror(kind MirrorKind) string {
	if ms := Mirrors(kind); len(ms) > 0 {
		return ms[0].URL
	}
	return ""
}
