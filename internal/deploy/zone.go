package deploy

import (
	"context"
	"fmt"
	"net"
	"strings"

	"interzone/internal/config"
)

// ZoneRecords are the DNS records the setup needs, as zone-file lines to
// paste into a DNS provider's import or copy one by one: an A (or AAAA)
// record for every hostname, at the server's address.
func ZoneRecords(ctx context.Context, c *config.Config) []string {
	var ips []string
	if h := c.Target.Host; c.Target.Mode == "ssh" && h != "" {
		if net.ParseIP(h) != nil {
			ips = []string{h}
		} else if found, err := net.DefaultResolver.LookupHost(ctx, h); err == nil {
			ips = found
		}
	}
	out := []string{"; DNS records for " + c.Domain + ": every name points at the server."}
	if len(ips) == 0 {
		out = append(out, "; Replace SERVER_IP with the server's public address.")
		ips = []string{"SERVER_IP"}
	} else if ip := net.ParseIP(ips[0]); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		out = append(out, "; "+ips[0]+" is a private address: use the server's public one instead.")
	}
	recs := c.DNSRecords()
	w := 0
	for _, r := range recs {
		w = max(w, len(r)+1)
	}
	for _, r := range recs {
		for _, ip := range ips {
			kind := "A"
			if strings.Contains(ip, ":") {
				kind = "AAAA"
			}
			out = append(out, fmt.Sprintf("%-*s 3600 IN %-4s %s", w, r+".", kind, ip))
		}
	}
	if !c.Components["base-domain"] {
		out = append(out, "", "; "+c.Domain+" itself can point anywhere, but must serve",
			"; https://"+c.Domain+"/.well-known/matrix/server and /client (Review checks them).")
	}
	return out
}
