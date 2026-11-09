package ios

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"testing"
)

func TestAppleRootCATrusted(t *testing.T) {
	b, _ := pem.Decode(appleRootCA)
	if b == nil {
		t.Fatal("apple-root-ca.pem holds no PEM block")
	}
	// The SHA-256 fingerprint of "Apple Root CA" in macOS's System Roots.
	const want = "b0b1730ecbc7ff4505142c49f1295e6eda6bcaed7e2c68c5be91b5a11001f024"
	if sum := sha256.Sum256(b.Bytes); hex.EncodeToString(sum[:]) != want {
		t.Fatalf("apple-root-ca.pem SHA-256 = %x, want %s", sum, want)
	}
	cert, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	cfg := http.DefaultTransport.(*http.Transport).TLSClientConfig
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("http.DefaultTransport has no RootCAs")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: cfg.RootCAs}); err != nil {
		t.Fatalf("http.DefaultTransport does not trust Apple Root CA: %v", err)
	}
}
