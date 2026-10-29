package config

import (
	"fmt"
	"strings"

	"interzone/internal/catalog"
)

// Port is what a firewall in front of the server must let in for one
// component: TCP and UDP ports, each a number or a range "a-b".
type Port struct {
	For      string
	TCP, UDP []string
}

// String is "80, 443/tcp; 443/udp".
func (p Port) String() string {
	var out []string
	if len(p.TCP) > 0 {
		out = append(out, strings.Join(p.TCP, ", ")+"/tcp")
	}
	if len(p.UDP) > 0 {
		out = append(out, strings.Join(p.UDP, ", ")+"/udp")
	}
	return strings.Join(out, "; ")
}

// Ports lists what a firewall in front of the server must let in, from the
// playbook's docs (prerequisites.md and the pages of the components that
// need more), with the port numbers the playbook's variables give, as
// vars.yml, extra vars or the playbook's defaults set them. Docker opens
// them on the server itself.
func (c *Config) Ports() []Port {
	on := func(id string) bool {
		comp, ok := catalog.ByID(id)
		return ok && c.Enabled(comp)
	}
	r := c.hostResolver()
	port := func(name, fallback string) string { return r.port(name, fallback) }
	span := func(from, to, a, b string) string { return port(from, a) + "-" + port(to, b) }

	out := []Port{{For: "Web (HTTPS, certificates)", TCP: []string{"80", "443"}, UDP: []string{"443"}}}
	// UVS and Matrix RTC need the federation port even with federation off.
	if fp := port("matrix_federation_public_port", c.FederationPort()); fp != "443" && (c.Federation || on("uvs") || on("matrix-rtc") || on("element-call")) {
		out = append(out, Port{For: "Federation", TCP: []string{fp}, UDP: []string{fp}})
	}
	if on("coturn") {
		plain, tls := port("coturn_container_stun_plain_host_bind_port_tcp", "3478"), port("coturn_container_stun_tls_host_bind_port_tcp", "5349")
		out = append(out, Port{For: "Coturn (TURN for calls)", TCP: []string{plain, tls},
			UDP: []string{plain, tls, span("coturn_turn_udp_min_port", "coturn_turn_udp_max_port", "49152", "49172")}})
	}
	if on("matrix-rtc") || on("element-call") {
		out = append(out, Port{For: "LiveKit (Element Call, Element X calls)",
			TCP: []string{port("livekit_server_config_rtc_tcp_port", "7881"), port("livekit_server_config_turn_tls_port", "5350")},
			UDP: []string{port("livekit_server_config_rtc_udp_port", "7882"), port("livekit_server_config_turn_udp_port", "3479"),
				span("livekit_server_config_turn_relay_range_start", "livekit_server_config_turn_relay_range_end", "30000", "30020")}})
	}
	if on("jitsi") {
		// 4443/tcp is the role's docs', with no variable behind it.
		out = append(out, Port{For: "Jitsi media", TCP: []string{"4443"}, UDP: []string{port("jitsi_jvb_rtp_udp_port", "10000")}})
	}
	if on("postmoogle") {
		out = append(out, Port{For: "Postmoogle (receiving email)",
			TCP: []string{port("matrix_bridge_postmoogle_smtp_host_bind_port", "25"), port("matrix_bridge_postmoogle_submission_host_bind_port", "587")}})
	}
	if c.Sources.IPFS && c.Sources.IPFSSeed == "yes" {
		out = append(out, Port{For: "IPFS (other servers fetch what this one seeds)", TCP: []string{"4001"}})
	}
	return out
}

// port is the value of name when it's a port number, or else fallback
// (unset, or a Jinja expression).
func (r hostResolver) port(name, fallback string) string {
	v := r.value(name, 0)
	if i := strings.LastIndex(v, ":"); i >= 0 { // "0.0.0.0:3478"
		v = v[i+1:]
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || fmt.Sprint(n) != v || n < 1 || n > 65535 {
		return fallback
	}
	return v
}
