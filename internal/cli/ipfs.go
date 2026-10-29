package cli

import (
	"flag"
	"fmt"
	"net/http"
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
