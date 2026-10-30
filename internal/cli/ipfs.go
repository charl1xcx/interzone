package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"interzone/internal/ipfs"
)

// ServeRegistry runs on the server during an IPFS deploy: Docker pulls the
// playbook's images from it, and it reads them through the local gateway.
// interzone starts and stops it; it isn't in Usage.
func ServeRegistry(args []string) error {
	fs := flag.NewFlagSet("serve-registry", flag.ExitOnError)
	layout := fs.String("layout", "", "URL of the OCI layout, e.g. http://127.0.0.1:8080/ipfs/CID/oci")
	listen := fs.String("listen", "127.0.0.1:5050", "address to serve the registry on")
	parse(fs, args)
	if *layout == "" {
		fs.Usage()
		return ErrFailed
	}
	r := &ipfs.Registry{Base: strings.TrimSuffix(*layout, "/"), Client: http.DefaultClient}
	if err := r.Load(); err != nil {
		return fmt.Errorf("reading the image list: %w", err)
	}
	return http.ListenAndServe(*listen, r)
}

// IPFSFollow runs on a hosting node from a systemd timer that interzone
// installs (Host for IPFS): it pins the newest signed publish and lets the
// previous one go. It isn't in Usage.
func IPFSFollow(args []string) error {
	fs := flag.NewFlagSet("ipfs-follow", flag.ExitOnError)
	api := fs.String("api", "http://127.0.0.1:45001", "the IPFS daemon's RPC API")
	parts := fs.String("parts", "all", "what to host: all, images, or some of oci,packages,git,bin")
	state := fs.String("state", filepath.Join(os.Getenv("STATE_DIRECTORY"), "follow.json"), "where to remember what is pinned")
	parse(fs, args)
	return ipfs.Follow(context.Background(), *api, *parts, *state, func(s string) { fmt.Println(s) })
}
