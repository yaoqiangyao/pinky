package ios

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net/http"
)

// appleRootCA is Apple's 2006 "Apple Root CA"
// (https://www.apple.com/appleca/AppleIncRootCertificate.cer). Apple's
// signing server, gs.apple.com, which signs the developer disk image for each
// iPhone, chains up to it. macOS and Windows trust it; the CA bundles most
// Linux systems ship (from Mozilla) do not, so without it every signing
// request fails with "x509: certificate signed by unknown authority".
//
//go:embed apple-root-ca.pem
var appleRootCA []byte

// Both signing requests, Pinky's own (iOS 27 and later) and go-ios's (iOS 17
// to 26), go through http.DefaultTransport, so trust Apple's root there, next
// to the system's roots. Only on Linux: on macOS and Windows setting RootCAs
// would switch off the platform's own verifier, which already trusts it.
func init() {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	roots.AppendCertsFromPEM(appleRootCA)
	t := http.DefaultTransport.(*http.Transport)
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	t.TLSClientConfig.RootCAs = roots
}
