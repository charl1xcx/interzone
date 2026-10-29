package config

import "interzone/internal/catalog"

// Rough memory each part takes once running, in MB: what the services use
// on a small server with a handful of users, not their peaks.
var (
	homeserverMB = map[string]int{"synapse": 1024, "dendrite": 512, "conduit": 256, "continuwuity": 256, "tuwunel": 256}
	componentMB  = map[string]int{
		"jitsi": 1536, "monitoring": 768, "media-repo": 256, "element-call": 384, "matrix-rtc": 384,
		"draupnir": 256, "mjolnir": 256, "meowlnir": 128, "hookshot": 192, "maubot": 160, "baibot": 128,
		"etherpad": 192, "mas": 128, "coturn": 64, "exim": 32, "sygnal": 64, "ntfy": 32, "goofys": 64,
		"corporal": 64, "pantalaimon": 128, "usage-exporter": 32, "auto-compressor": 64, "borg": 128,
		// settings or files, not services of their own
		"bridges-e2ee": 0, "bridges-relay": 0, "double-puppet": 0, "rest-auth": 0, "shared-secret-auth": 0,
		"ldap-auth": 0, "simple-antispam": 0, "telemetry": 0, "base-domain": 0, "s3-provider": 0,
		"cleanup": 0, "external-postgres": 0, "ddclient": 8, "uvs": 64, "ketesa": 16, "element-admin": 16,
	}
	categoryMB = map[string]int{"Bridges": 128, "Bots": 96, "Clients": 16}
)

// Estimate is about how much memory (MB) and disk (GB) the setup needs to
// run, before any media or messages: the homeserver, Postgres, Traefik and
// every enabled component, and the container images on disk.
func (c *Config) Estimate() (memMB, diskGB int) {
	memMB = homeserverMB[c.Homeserver] + 64 // Traefik and the rest of the base
	if !c.Components["external-postgres"] {
		memMB += 256
	}
	images := 4
	for _, comp := range catalog.Components {
		if !c.Enabled(comp) {
			continue
		}
		mb, ok := componentMB[comp.ID]
		if !ok {
			mb = categoryMB[comp.Category]
			if mb == 0 {
				mb = 64
			}
		}
		memMB += mb
		if mb > 0 {
			images++
		}
	}
	// About 300 MB of images each, and room for the database and logs to start.
	return memMB, 5 + images*3/10
}
