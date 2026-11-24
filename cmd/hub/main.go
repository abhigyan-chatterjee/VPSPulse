package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"vpspulse/internal/hub"
	"vpspulse/internal/store"
	"vpspulse/web"
)

func main() {
	defaultListen := envOr("VPSP_LISTEN", ":8080")
	defaultDB := envOr("VPSP_DB_PATH", "./data/vpspulse.db")
	defaultPublicURL := os.Getenv("VPSP_PUBLIC_URL")
	defaultDownloads := envOr("VPSP_DOWNLOADS_DIR", "./bin")
	defaultTrustProxy := os.Getenv("VPSP_TRUST_PROXY") == "true" || os.Getenv("VPSP_TRUST_PROXY") == "1"

	listen := flag.String("listen", defaultListen, "HTTP listen address")
	dbPath := flag.String("db", defaultDB, "SQLite database path")
	publicURL := flag.String("public-url", defaultPublicURL, "Public hub URL for install scripts and links")
	trustProxy := flag.Bool("trust-proxy", defaultTrustProxy, "Trust X-Forwarded-Proto from reverse proxy")
	tlsCert := flag.String("tls-cert", os.Getenv("VPSP_TLS_CERT"), "Optional TLS certificate path")
	tlsKey := flag.String("tls-key", os.Getenv("VPSP_TLS_KEY"), "Optional TLS key path")
	downloadsDir := flag.String("downloads-dir", defaultDownloads, "Directory containing vpspulse-agent binaries")
	demo := flag.Bool("demo", false, "Run in demo mode with simulated VPSes and temporary SQLite DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	actualDB := *dbPath
	if *demo {
		tmpDir, err := os.MkdirTemp("", "vpspulse-demo-*")
		if err != nil {
			log.Fatalf("create demo temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		actualDB = filepath.Join(tmpDir, "demo.db")
	}

	st, err := store.Open(actualDB)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = st.Close() }()

	if adminUser, adminPass := os.Getenv("VPSP_ADMIN_USER"), os.Getenv("VPSP_ADMIN_PASSWORD"); adminUser != "" && adminPass != "" {
		if has, _ := st.HasAdmin(); !has {
			if _, err := st.CreateAdmin(adminUser, adminPass); err != nil {
				log.Printf("warning: failed to auto-create admin from env: %v", err)
			}
		}
	}

	broker := hub.NewBroker()
	alerts := hub.NewAlertManager(st, broker)
	prober := hub.NewProber(st, broker, alerts)

	if *demo {
		if err := hub.SeedAndStartDemo(ctx, st, broker, alerts); err != nil {
			log.Fatalf("seed demo mode: %v", err)
		}
		log.Println("DEMO MODE enabled — login with admin / demo1234")
	}

	staticFS, err := web.DistFS()
	if err != nil {
		log.Fatalf("load embedded web assets: %v", err)
	}

	srv := hub.NewServer(hub.Config{
		PublicURL:    *publicURL,
		TrustProxy:   *trustProxy,
		DownloadsDir: *downloadsDir,
		DemoMode:     *demo,
	}, st, broker, prober, alerts, staticFS)

	go prober.Start(ctx)
	go alerts.Start(ctx)

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shCtx)
	}()

	log.Printf("vpspulse-hub listening on %s (db=%s)", *listen, actualDB)
	if *tlsCert != "" && *tlsKey != "" {
		err = httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
	} else {
		err = httpSrv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server error: %v", err)
	}
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
