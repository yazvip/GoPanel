package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopanel/internal/acme"
	"gopanel/internal/apps"
	"gopanel/internal/audit"
	"gopanel/internal/auth"
	"gopanel/internal/backup"
	"gopanel/internal/certs"
	"gopanel/internal/config"
	"gopanel/internal/cronjobs"
	"gopanel/internal/databases"
	"gopanel/internal/db"
	"gopanel/internal/files"
	"gopanel/internal/httpapi"
	"gopanel/internal/jobs"
	"gopanel/internal/metrics"
	"gopanel/internal/model"
	"gopanel/internal/scheduler"
	"gopanel/internal/sites"
	"gopanel/internal/store"
	"gopanel/internal/sys/factory"
	"gopanel/internal/terminal"
	"gopanel/internal/update"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gopanel: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", "config.json", "path to the configuration file")
		dev         = flag.Bool("dev", false, "development mode with fake system data")
		listen      = flag.String("listen", "", "override the listen address")
		showVersion = flag.Bool("version", false, "print the version and exit")
		resetAdmin  = flag.Bool("reset-admin", false, "reset the administrator password and exit (stop the service first)")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*configPath, *dev)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	key, err := cfg.EnsureSecretKey()
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	st := store.New(database)

	if *resetAdmin {
		return resetAdminPassword(st)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := st.MarkJobsInterrupted(ctx); err != nil {
		logger.Warn("mark interrupted jobs", "error", err)
	}

	system := factory.New(cfg)
	backupService, err := backup.New(st, system, logger, backup.Options{Dir: cfg.BackupDir, SiteRootBase: cfg.SiteRootBase})
	if err != nil {
		return fmt.Errorf("backup service: %w", err)
	}
	backupService.MarkInterrupted(ctx)
	authService := auth.New(st, key, secureCookies(cfg), time.Duration(cfg.SessionIdleHours)*time.Hour, time.Duration(cfg.SessionAbsoluteHours)*time.Hour)

	adminUser, adminPassword, created, err := authService.EnsureFirstAdmin(ctx, cfg.Dev, os.Getenv("GOPANEL_ADMIN_PASSWORD"))
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if created {
		// The password is deliberately not logged: it is printed to stdout
		// only, so it can be captured by the installer without ending up in
		// the journal.
		logger.Warn("created the first administrator", "username", adminUser, "must_change_password", !cfg.Dev)
		fmt.Printf("\n  GoPanel first administrator\n  username: %s\n  password: %s\n\n", adminUser, adminPassword)
	}

	metricsService := metrics.New(st, system, logger, metrics.Options{})
	metricsService.Start(ctx)

	roots := make([]files.Root, 0, len(cfg.AllowedRoots))
	for _, root := range cfg.AllowedRoots {
		roots = append(roots, files.Root{Token: root.Token, Label: root.Label, Path: root.Path})
	}
	fileService := files.New(roots, cfg.UploadMaxBytes)

	panelPort := listenPort(cfg.Listen)
	siteService := sites.New(st, system, sites.Options{
		SiteRootBase: cfg.SiteRootBase,
		NginxLogDir:  cfg.NginxLogDir,
		ACMEWebroot:  cfg.ACMEWebroot,
		PanelPort:    panelPort,
	})

	jobManager := jobs.New(st, cfg.LogDir, 3, logger)
	cronService := cronjobs.New(st, system, logger, cronjobs.Options{CronFile: cfg.CronFile, LogDir: cfg.LogDir})
	appService := apps.New(system, logger)

	acmeService := acme.New(acme.Config{
		Webroot:    cfg.ACMEWebroot,
		AccountDir: filepath.Join(cfg.DataDir, "acme", "accounts"),
		SSLDir:     cfg.SSLDir,
		Email:      cfg.ACMEEmail,
		Staging:    cfg.ACMEStaging,
	}, logger)
	certService := certs.New(st, siteService, acmeService, certs.Options{Email: cfg.ACMEEmail, Staging: cfg.ACMEStaging, Logger: logger})
	databaseService := databases.New(st, system, authService, logger)
	auditRecorder := audit.New(st, logger)
	terminalManager := terminal.New()
	updateService := update.New(update.Options{
		Version:    version,
		ConfigPath: cfg.ConfigPath,
		Logger:     logger,
	})

	for name, register := range map[string]func(*jobs.Manager) error{
		"backup": backupService.RegisterJobs,
		"cron":   cronService.RegisterJobs,
		"apps":   appService.RegisterJobs,
		"certs":  certService.RegisterJobs,
	} {
		if err := register(jobManager); err != nil {
			return fmt.Errorf("register %s jobs: %w", name, err)
		}
	}
	jobManager.Start()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := jobManager.Shutdown(shutdownCtx); err != nil {
			logger.Warn("job manager shutdown", "error", err)
		}
	}()

	schedulerService, err := scheduler.New(scheduler.Options{
		Store:  st,
		Backup: backupService,
		Certs:  certService,
		Jobs:   jobManager,
		Audit:  auditRecorder,
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("scheduler: %w", err)
	}
	schedulerService.Start()
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := schedulerService.Stop(stopCtx); err != nil {
			logger.Warn("scheduler shutdown", "error", err)
		}
	}()

	if err := cronService.Sync(ctx); err != nil {
		logger.Warn("sync cron file", "error", err)
	}

	api, err := httpapi.New(httpapi.Deps{
		Config:    cfg,
		Store:     st,
		Auth:      authService,
		Metrics:   metricsService,
		Files:     fileService,
		Sites:     siteService,
		Backups:   backupService,
		Cron:      cronService,
		Apps:      appService,
		Jobs:      jobManager,
		Certs:     certService,
		Databases: databaseService,
		ACME:      acmeService,
		Terminal:  terminalManager,
		Audit:     auditRecorder,
		Update:    updateService,
		System:    system,
		Logger:    logger,
		Version:   version,
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("gopanel listening", "address", cfg.Listen, "version", version, "dev", cfg.Dev)
		certFile, keyFile, tlsErr := resolveTLS(ctx, st, cfg)
		if tlsErr != nil {
			logger.Warn("TLS configuration ignored", "error", tlsErr)
		}
		if certFile != "" && keyFile != "" {
			serverErr <- server.ListenAndServeTLS(certFile, keyFile)
			return
		}
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "error", err)
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func secureCookies(cfg *config.Config) bool {
	if cfg.TLS.CertFile != "" || strings.HasPrefix(strings.ToLower(cfg.PanelURL), "https://") {
		return true
	}
	return false
}

func listenPort(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}

func resolveTLS(ctx context.Context, st *store.Store, cfg *config.Config) (string, string, error) {
	certFile, keyFile := cfg.TLS.CertFile, cfg.TLS.KeyFile
	if certFile == "" || keyFile == "" {
		if v, ok, _ := st.GetSetting(ctx, "tls_cert_file"); ok && v != "" {
			certFile = v
		}
		if v, ok, _ := st.GetSetting(ctx, "tls_key_file"); ok && v != "" {
			keyFile = v
		}
	}
	if certFile == "" || keyFile == "" {
		return "", "", nil
	}
	if _, err := os.Stat(certFile); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(keyFile); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// resetAdminPassword implements `gopanel -reset-admin`: it sets a new password
// for the administrator account, forces a password change at next login, and
// invalidates every existing session. It is meant to be run from a root shell
// while the panel service is stopped.
func resetAdminPassword(st *store.Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	password := os.Getenv("GOPANEL_ADMIN_PASSWORD")
	if password == "" {
		password = auth.GeneratePassword(16)
		if password == "" {
			return errors.New("could not generate a random password")
		}
	}

	user, err := st.GetUserByUsername(ctx, "admin")
	if err != nil {
		users, listErr := st.ListUsers(ctx)
		if listErr != nil {
			return fmt.Errorf("look up administrator: %w", err)
		}
		var found *model.User
		for i := range users {
			if users[i].Role == "admin" {
				found = &users[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("no administrator account found: %w", err)
		}
		user = found
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := st.UpdatePassword(ctx, user.ID, hash, true); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if err := st.DeleteUserSessions(ctx, user.ID, ""); err != nil {
		return fmt.Errorf("invalidate sessions: %w", err)
	}

	fmt.Printf("\n  GoPanel administrator password reset\n  username: %s\n  password: %s\n\n", user.Username, password)
	return nil
}
