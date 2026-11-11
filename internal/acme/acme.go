package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"gopanel/internal/model"
)

var (
	ErrDisabled    = errors.New("ACME is not configured")
	ErrDNSMismatch = errors.New("domain does not resolve to this server")
)

type Config struct {
	Webroot    string
	AccountDir string
	SSLDir     string
	Email      string
	Staging    bool
}

type Service struct {
	cfg    Config
	logger *slog.Logger
	mu     sync.Mutex
}

type Result struct {
	CertPath  string
	KeyPath   string
	ExpiresAt time.Time
	Staging   bool
}

func New(cfg Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, logger: logger}
}

func (s *Service) Configured() bool {
	return s.cfg.Webroot != "" && s.cfg.AccountDir != "" && s.cfg.SSLDir != ""
}

func (s *Service) Email() string { return s.cfg.Email }

type accountUser struct {
	Email string
	key   crypto.Signer
	reg   *registration.Resource
}

func (u *accountUser) GetEmail() string                        { return u.Email }
func (u *accountUser) GetRegistration() *registration.Resource { return u.reg }
func (u *accountUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

type webrootProvider struct {
	dir string
}

func (p *webrootProvider) Present(domain, token, keyAuth string) error {
	if token == "" || strings.ContainsAny(token, `/\`) || token != filepath.Base(token) {
		return fmt.Errorf("invalid challenge token")
	}
	dir := filepath.Join(p.dir, ".well-known", "acme-challenge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(p.dir, ".well-known"), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, token)
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(keyAuth), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Chmod(file, 0o644)
}

func (p *webrootProvider) CleanUp(domain, token, keyAuth string) error {
	if token == "" || token != filepath.Base(token) {
		return nil
	}
	err := os.Remove(filepath.Join(p.dir, ".well-known", "acme-challenge", token))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Service) accountPath(staging bool) string {
	env := "production"
	if staging {
		env = "staging"
	}
	return filepath.Join(s.cfg.AccountDir, "account-"+env+".json")
}

func (s *Service) keyPath(staging bool) string {
	env := "production"
	if staging {
		env = "staging"
	}
	return filepath.Join(s.cfg.AccountDir, "account-"+env+".key")
}

func (s *Service) loadUser(staging bool, email string) (*accountUser, error) {
	user := &accountUser{Email: email}
	keyFile := s.keyPath(staging)
	regData, regErr := os.ReadFile(s.accountPath(staging))
	keyData, keyErr := os.ReadFile(keyFile)
	if regErr == nil {
		if keyErr != nil {
			return nil, fmt.Errorf("ACME registration exists but its key is missing: %w", keyErr)
		}
		block, _ := pem.Decode(keyData)
		if block == nil {
			return nil, fmt.Errorf("account key is not valid PEM")
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse account key: %w", err)
		}
		var reg registration.Resource
		if err := json.Unmarshal(regData, &reg); err != nil {
			return nil, fmt.Errorf("parse account registration: %w", err)
		}
		user.key = key
		user.reg = &reg
		return user, nil
	}
	if !errors.Is(regErr, os.ErrNotExist) {
		return nil, regErr
	}
	// Without a saved registration there is no KID to reuse, and registering
	// again with the old key would collide with the existing account. Always
	// start a fresh account key in that case.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	user.key = key
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Service) saveRegistration(staging bool, reg *registration.Resource) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.accountPath(staging), data, 0o600)
}

func writeFile(name string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), ".gopanel-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, name)
}

func (s *Service) client(staging bool, email string) (*lego.Client, *accountUser, error) {
	if !s.Configured() {
		return nil, nil, ErrDisabled
	}
	if err := os.MkdirAll(s.cfg.AccountDir, 0o700); err != nil {
		return nil, nil, err
	}
	user, err := s.loadUser(staging, email)
	if err != nil {
		return nil, nil, err
	}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = lego.LEDirectoryProduction
	if staging {
		cfg.CADirURL = lego.LEDirectoryStaging
	}
	cfg.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	cfg.Certificate.Timeout = 90 * time.Second
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := client.Challenge.SetHTTP01Provider(&webrootProvider{dir: s.cfg.Webroot}); err != nil {
		return nil, nil, err
	}
	if user.reg == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, nil, fmt.Errorf("register ACME account: %w", err)
		}
		user.reg = reg
		if err := s.saveRegistration(staging, reg); err != nil {
			return nil, nil, err
		}
	}
	return client, user, nil
}

func (s *Service) Issue(ctx context.Context, site *model.Site, domains []string, staging bool, email string) (*Result, error) {
	if len(domains) == 0 {
		return nil, fmt.Errorf("no domains supplied")
	}
	for _, d := range domains {
		if d == "" || strings.ContainsAny(d, " /\\") {
			return nil, fmt.Errorf("invalid domain %q", d)
		}
	}
	if email == "" {
		email = s.cfg.Email
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	client, _, err := s.client(staging, email)
	if err != nil {
		return nil, err
	}
	type obtainResult struct {
		cert *certificate.Resource
		err  error
	}
	ch := make(chan obtainResult, 1)
	go func() {
		cert, err := client.Certificate.Obtain(certificate.ObtainRequest{
			Domains: domains,
			Bundle:  true,
		})
		ch <- obtainResult{cert: cert, err: err}
	}()
	var res obtainResult
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res = <-ch:
	}
	if res.err != nil {
		return nil, fmt.Errorf("obtain certificate: %w", res.err)
	}
	if len(res.cert.Certificate) == 0 || len(res.cert.PrivateKey) == 0 {
		return nil, fmt.Errorf("ACME returned an empty certificate")
	}
	expires, err := certificateExpiry(res.cert.Certificate)
	if err != nil {
		return nil, err
	}
	slug := site.Name
	if slug == "" {
		slug = domains[0]
	}
	if strings.ContainsAny(slug, "/\\..") {
		return nil, fmt.Errorf("invalid site slug")
	}
	dir := filepath.Join(s.cfg.SSLDir, slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := writeFile(certPath, res.cert.Certificate, 0o644); err != nil {
		return nil, err
	}
	if err := writeFile(keyPath, res.cert.PrivateKey, 0o600); err != nil {
		return nil, err
	}
	s.logger.Info("certificate issued", "site", slug, "domains", strings.Join(domains, ","), "staging", staging, "expires", expires)
	return &Result{CertPath: certPath, KeyPath: keyPath, ExpiresAt: expires, Staging: staging}, nil
}

func ExpiryFile(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	return certificateExpiry(data)
}

func certificateExpiry(chain []byte) (time.Time, error) {
	block, _ := pem.Decode(chain)
	if block == nil {
		return time.Time{}, fmt.Errorf("certificate is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

func (s *Service) Remove(siteName string) error {
	if strings.ContainsAny(siteName, "/\\..") {
		return fmt.Errorf("invalid site name")
	}
	return os.RemoveAll(filepath.Join(s.cfg.SSLDir, siteName))
}

func (s *Service) CheckDNS(ctx context.Context, domains []string, publicIP string) ([]string, error) {
	ip := net.ParseIP(strings.TrimSpace(publicIP))
	if ip == nil {
		return nil, nil
	}
	resolver := net.DefaultResolver
	var warnings []string
	for _, domain := range domains {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		addrs, err := resolver.LookupHost(lookupCtx, domain)
		cancel()
		if err != nil || len(addrs) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s does not resolve yet", domain))
			continue
		}
		found := false
		for _, addr := range addrs {
			if parsed := net.ParseIP(addr); parsed != nil && parsed.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			warnings = append(warnings, fmt.Sprintf("%s resolves to %s, not %s", domain, strings.Join(addrs, ", "), ip))
		}
	}
	return warnings, nil
}

// Ensure interface compliance at compile time.
var _ challenge.Provider = (*webrootProvider)(nil)
