package config

import "interzone/internal/catalog"

// Port is a port (or range) the server must accept from the internet, and
// what for.
type Port struct{ Port, For string }

// Ports lists what a firewall in front of the server must let in, from the
// playbook's docs (prerequisites.md and the pages of the components that
// need more). Docker opens them on the server itself.
// shortcut: the playbook's default port numbers; a port moved in extra vars isn't followed.
func (c *Config) Ports() []Port {
	on := func(id string) bool {
		comp, ok := catalog.ByID(id)
		return ok && c.Enabled(comp)
	}
	out := []Port{{"80/tcp, 443/tcp+udp", "Web (HTTPS, certificates)"}}
	// UVS and Matrix RTC need the federation port even with federation off.
	if fp := c.FederationPort(); fp != "443" && (c.Federation || on("uvs") || on("matrix-rtc") || on("element-call")) {
		out = append(out, Port{fp + "/tcp+udp", "Federation"})
	}
	if on("coturn") {
		out = append(out, Port{"3478, 5349/tcp+udp; 49152-49172/udp", "Coturn (TURN for calls)"})
	}
	if on("matrix-rtc") || on("element-call") {
		out = append(out, Port{"7881/tcp, 5350/tcp; 7882, 3479, 30000-30020/udp", "LiveKit (Element Call, Element X calls)"})
	}
	if on("jitsi") {
		out = append(out, Port{"4443/tcp, 10000/udp", "Jitsi media"})
	}
	if on("postmoogle") {
		out = append(out, Port{"25/tcp, 587/tcp", "Postmoogle (receiving email)"})
	}
	return out
}
