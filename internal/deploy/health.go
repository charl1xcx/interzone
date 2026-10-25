package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

// Health is whether the homeserver answers over HTTPS, seen from here.
type Health struct {
	Up       bool
	Err      string        // why it isn't up
	CertLeft time.Duration // until the certificate expires
	Trusted  bool          // the certificate verifies against the system roots
}

// CheckHealth asks https://<host>/_matrix/client/versions. Certificates
// aren't verified for the request itself, so a self-signed server still
// counts as up; Trusted says whether it would pass.
func CheckHealth(ctx context.Context, host string) Health {
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           http.ProxyFromEnvironment,
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/_matrix/client/versions", nil)
	if err != nil {
		return Health{Err: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Health{Err: netReason(err)}
	}
	resp.Body.Close()
	var h Health
	if cs := resp.TLS; cs != nil && len(cs.PeerCertificates) > 0 {
		leaf := cs.PeerCertificates[0]
		h.CertLeft = time.Until(leaf.NotAfter)
		inter := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		_, verr := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter})
		h.Trusted = verr == nil
	}
	if resp.StatusCode != http.StatusOK {
		h.Err = "answered " + resp.Status
		return h
	}
	h.Up = true
	return h
}

// netReason says in a few words why a request failed.
func netReason(err error) string {
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		return "the name doesn't resolve"
	case errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err):
		return "timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH):
		return "no route to the server"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // without the method and URL
	}
	return err.Error()
}
