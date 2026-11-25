package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"vpspulse/internal/agent"
	"vpspulse/internal/version"
)

func main() {
	defaultURL := os.Getenv("VPSP_HUB_URL")
	defaultToken := os.Getenv("VPSP_AGENT_TOKEN")
	defaultSocket := os.Getenv("VPSP_DOCKER_SOCKET")
	defaultInterval := 10
	if raw := os.Getenv("VPSP_INTERVAL"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 5 && n <= 300 {
			defaultInterval = n
		}
	}

	hubURL := flag.String("url", defaultURL, "VPSPulse hub base URL (e.g. https://monitor.example.com)")
	token := flag.String("token", defaultToken, "Per-VPS agent bearer token")
	interval := flag.Int("interval", defaultInterval, "Heartbeat interval in seconds (5-300)")
	dockerSocket := flag.String("docker-socket", defaultSocket, "Path to Docker Unix socket")
	once := flag.Bool("once", false, "Send a single heartbeat and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("vpspulse-agent", version.String())
		return
	}

	if *hubURL == "" || *token == "" {
		log.Fatal("both --url (or VPSP_HUB_URL) and --token (or VPSP_AGENT_TOKEN) are required")
	}

	u, err := url.Parse(*hubURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Fatalf("invalid --url %q: must be an http:// or https:// URL", *hubURL)
	}

	runner := agent.NewRunner(*hubURL, *token, *dockerSocket, version.String(), time.Duration(*interval)*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		onceCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := runner.SendOnce(onceCtx); err != nil {
			log.Fatalf("heartbeat failed: %v", err)
		}
		log.Println("heartbeat sent successfully")
		return
	}

	log.Printf("vpspulse-agent v%s started -> %s (interval=%s)", version.String(), *hubURL, runner.Interval)
	runner.Run(ctx, func(err error) {
		log.Printf("heartbeat error: %v", err)
	})
	log.Println("vpspulse-agent stopped")
}
