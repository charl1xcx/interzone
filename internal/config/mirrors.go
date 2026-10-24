package config

// MirrorKind says what a mirror serves.
type MirrorKind string

const (
	MirrorAPT    MirrorKind = "apt"    // /ubuntu, /debian and /debian-security
	MirrorDocker MirrorKind = "docker" // a copy of download.docker.com/linux
	MirrorPyPI   MirrorKind = "pypi"   // a PyPI simple index
)

type Mirror struct {
	Kind MirrorKind
	Name string
	URL  string
}

// KnownMirrors lists public mirrors hosted in Iran, best first. Each one was
// checked to serve fresh packages (October 2026); `interzone mirrors` and
// the network tests in internal/deploy re-check them.
var KnownMirrors = []Mirror{
	{MirrorAPT, "ArvanCloud", "https://mirror.arvancloud.ir"},
	{MirrorAPT, "Runflare", "https://mirror-linux.runflare.com"},
	{MirrorAPT, "IranServer", "https://mirror.iranserver.com"},
	{MirrorAPT, "Hostbaran", "https://mirror.hostbaran.com"},
	{MirrorAPT, "Abrha", "https://repo.abrha.net"},
	{MirrorAPT, "MobinHost", "https://mirror.mobinhost.com"},

	{MirrorDocker, "Abrha", "https://repo.abrha.net/docker"},
	{MirrorDocker, "CDN.ir", "https://mirror.cdn.ir/repository/docker/linux"},

	{MirrorPyPI, "Runflare", "https://mirror-pypi.runflare.com/simple"},
	{MirrorPyPI, "IranServer", "https://pypi.iranserver.com/repository/pypi/simple"},
	{MirrorPyPI, "Liara", "https://package-mirror.liara.ir/repository/pypi/simple"},
	{MirrorPyPI, "Abrha", "https://mirror.abrha.net/repository/pypi/simple"},
	{MirrorPyPI, "Chabokan", "https://mirror2.chabokan.net/pypi/simple"},
}

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

// DefaultMirror is the URL blackout mode starts with for kind.
func DefaultMirror(kind MirrorKind) string {
	if ms := Mirrors(kind); len(ms) > 0 {
		return ms[0].URL
	}
	return ""
}
