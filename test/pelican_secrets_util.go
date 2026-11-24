package test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/masterminds/sprig"
)

// pelicanTLSCerts holds a generated CA and leaf TLS certificate+key as PEM strings.
type pelicanTLSCerts struct {
	caCert  string
	tlsCert string
	tlsKey  string
}

// pelicanIssuerKeys holds PEM-encoded private keys for each Pelican service.
type pelicanIssuerKeys struct {
	director string
	registry string
	origin   string
	cache    string
}

// pelicanSecretsData is the complete set of values needed to render the
// manifests/util/pelican-secrets Go templates.
type pelicanSecretsData struct {
	CaCert            string
	TlsCert           string
	TlsKey            string
	OidcClientId      string
	OidcClientSecret  string
	ServerWebPasswd   string
	DirectorIssuerKey string
	RegistryIssuerKey string
	OriginIssuerKey   string
	CacheIssuerKey    string
}

// generatePelicanTLSCerts creates a self-signed CA and a leaf certificate
// covering the DNS names used by Pelican services inside the cluster, plus
// localhost for out-of-cluster access.
func generatePelicanTLSCerts() (pelicanTLSCerts, error) {
	// --- CA key + cert ---
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return pelicanTLSCerts{}, err
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pelican-test-framework-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return pelicanTLSCerts{}, err
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return pelicanTLSCerts{}, err
	}

	// --- Leaf key + cert ---
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return pelicanTLSCerts{}, err
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "pelican-test-framework-server"},
		DNSNames: []string{
			"localhost",
			"localhost.localdomain",
			"director",
			"registry",
			"origin",
			"cache",
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return pelicanTLSCerts{}, err
	}

	// --- PEM encode ---
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return pelicanTLSCerts{}, err
	}
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	return pelicanTLSCerts{
		caCert:  string(caCertPEM),
		tlsCert: string(leafCertPEM),
		tlsKey:  string(leafKeyPEM),
	}, nil
}

// generatePelicanIssuerKeys creates the service signing keys in a temporary pod
// using the same registry image version as the services under test.
func generatePelicanIssuerKeys(t *testing.T, options *k8s.KubectlOptions, serverImage string) (keys pelicanIssuerKeys, retErr error) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "pelican-issuer-keys-*")
	if err != nil {
		return pelicanIssuerKeys{}, fmt.Errorf("create temporary key directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	podName := "pelican-keygen-" + strings.ToLower(random.UniqueId())
	defer func() {
		if err := k8s.RunKubectlE(t, options, "delete", "pod", podName, "--ignore-not-found=true", "--wait=true"); err != nil {
			cleanupErr := fmt.Errorf("delete temporary Pelican key-generation pod %s: %w", podName, err)
			if retErr == nil {
				retErr = cleanupErr
			} else {
				t.Logf("Failed to clean up temporary Pelican key-generation pod: %v", cleanupErr)
			}
		}
	}()

	if err := k8s.RunKubectlE(t, options, "run", podName,
		"--image="+serverImage,
		"--restart=Never",
		"--command", "--", "sleep", "infinity",
	); err != nil {
		return pelicanIssuerKeys{}, fmt.Errorf("start Pelican key-generation pod using %s: %w", serverImage, err)
	}
	if err := k8s.RunKubectlE(t, options, "wait", "--for=condition=Ready", "pod/"+podName, "--timeout=2m"); err != nil {
		return pelicanIssuerKeys{}, fmt.Errorf("wait for Pelican key-generation pod %s: %w", podName, err)
	}

	containerKeyDir := "/tmp/pelican-issuer-keys"
	if _, err := k8s.ExecPodE(t, options, podName, "", "mkdir", "-p", containerKeyDir); err != nil {
		return pelicanIssuerKeys{}, fmt.Errorf("create key directory in pod %s: %w", podName, err)
	}

	services := []string{"director", "registry", "origin", "cache"}
	pemContents := make(map[string]string, len(services))

	for _, svc := range services {
		privKeyPath := filepath.Join(tmpDir, svc+".pem")
		containerPrivKeyPath := filepath.Join(containerKeyDir, svc+".pem")
		containerPubKeyPath := filepath.Join(containerKeyDir, svc+".jwks")
		out, err := k8s.ExecPodE(t, options, podName, "",
			"pelican-server", "key", "create",
			"--private-key", containerPrivKeyPath,
			"--public-key", containerPubKeyPath,
		)
		if err != nil {
			return pelicanIssuerKeys{}, fmt.Errorf("create issuer key for %s in pod %s: %w (output: %s)", svc, podName, err, out)
		}

		if err := k8s.RunKubectlE(t, options, "cp", podName+":"+containerPrivKeyPath, privKeyPath); err != nil {
			return pelicanIssuerKeys{}, fmt.Errorf("copy issuer key for %s from pod %s: %w", svc, podName, err)
		}
		data, err := os.ReadFile(privKeyPath)
		if err != nil {
			return pelicanIssuerKeys{}, fmt.Errorf("read issuer key for %s: %w", svc, err)
		}
		pemContents[svc] = string(data)
	}

	return pelicanIssuerKeys{
		director: pemContents["director"],
		registry: pemContents["registry"],
		origin:   pemContents["origin"],
		cache:    pemContents["cache"],
	}, nil
}

// applySprigTemplate executes the Go template at templatePath with sprig functions
// available and the given data, returning the rendered result as a string.
// Fails the test on any error.
func applySprigTemplate(t *testing.T, templatePath string, data any, funcMap template.FuncMap) string {
	t.Helper()
	var b strings.Builder
	base := template.New(filepath.Base(templatePath)).Funcs(sprig.TxtFuncMap())
	if funcMap != nil {
		base = base.Funcs(funcMap)
	}
	templ := template.Must(base.ParseFiles(templatePath))
	if err := templ.ExecuteTemplate(&b, filepath.Base(templatePath), data); err != nil {
		t.Fatalf("Failed to template k8s manifest %s: %v", templatePath, err)
	}
	return b.String()
}

// applyPelicanSecrets generates all required TLS certs and issuer keys, renders
// the manifests/util/pelican-secrets templates, and applies them to the cluster. Returns
// the rendered manifest string for later cleanup via deletePelicanSecrets.
//
// oidcClientId and oidcClientSecret may be empty strings if OIDC is not
// required by the test. serverWebPasswd may also be empty to use Pelican's
// default password behaviour.
func (th *TestHandle) applyPelicanSecrets(oidcClientId, oidcClientSecret, serverWebPasswd, serverImage string) string {
	th.T.Helper()

	certs, err := generatePelicanTLSCerts()
	if err != nil {
		th.T.Fatalf("Failed to generate Pelican TLS certs: %v", err)
	}

	keys, err := generatePelicanIssuerKeys(th.T, th.options, serverImage)
	if err != nil {
		th.T.Fatalf("Failed to generate Pelican issuer keys: %v", err)
	}

	data := pelicanSecretsData{
		CaCert:            certs.caCert,
		TlsCert:           certs.tlsCert,
		TlsKey:            certs.tlsKey,
		OidcClientId:      oidcClientId,
		OidcClientSecret:  oidcClientSecret,
		ServerWebPasswd:   serverWebPasswd,
		DirectorIssuerKey: keys.director,
		RegistryIssuerKey: keys.registry,
		OriginIssuerKey:   keys.origin,
		CacheIssuerKey:    keys.cache,
	}

	// Render all five secret template files and concatenate them.
	templateDir := "../manifests/util/pelican-secrets"
	templateFiles := []string{
		"tls-secrets.yaml",
		"director-issuer-key.yaml",
		"registry-issuer-key.yaml",
		"origin-issuer-key.yaml",
		"cache-issuer-key.yaml",
	}

	var sb strings.Builder
	for _, fname := range templateFiles {
		rendered := applySprigTemplate(th.T, filepath.Join(templateDir, fname), data, nil)
		sb.WriteString(rendered)
		sb.WriteString("\n")
	}

	manifest := sb.String()
	k8s.KubectlApplyFromString(th.T, th.options, manifest)

	return manifest
}

// deletePelicanSecrets removes the secrets previously applied by applyPelicanSecrets.
func (th *TestHandle) deletePelicanSecrets(manifest string) {
	th.T.Helper()
	k8s.KubectlDeleteFromString(th.T, th.options, manifest)
}
