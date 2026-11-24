package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"vpspulse/internal/agent"
)

var version = "0.1.0"

func main() {
	defaultURL := os.Getenv("VPSP_HUB_URL")
	defaultToken := os.Getenv("VPSP_AGENT_TOKEN")
	defaultSocket := os.Getenv("VPSP_DOCKER_SOCKET")
	if defaultSocket == "" {
		defaultSocket = "/var/run/docker.sock"
	}
	defaultInterval := 10
	if raw := os.Getenv("VPSP_INTERVAL"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 5 {
			defaultInterval = n
		}
	}

	hubURL := flag.String("url", defaultURL, "VPSPulse hub base URL (e.g. https://monitor.example.com)")
	token := flag.String("token", defaultToken, "Per-VPS agent bearer token")
	interval := flag.Int("interval", defaultInterval, "Heartbeat interval in seconds")
	dockerSocket := flag.String("docker-socket", defaultSocket, "Path to Docker Unix socket")
	once := flag.Bool("once", false, "Send a single heartbeat and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("vpspulse-agent", version)
		return
	}

	if *hubURL == "" || *token == "" {
		log.Fatal("both --url (or VPSP_HUB_URL) and --token (or VPSP_AGENT_TOKEN) are required")
	}

	runner := agent.NewRunner(*hubURL, *token, *dockerSocket, version, time.Duration(*interval)*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		if err := runner.SendOnce(ctx); err != nil {
			log.Fatalf("heartbeat failed: %v", err)
		}
		log.Println("heartbeat sent successfully")
		return
	}

	log.Printf("vpspulse-agent v%s started -> %s (interval=%ds)", version, *hubURL, *interval)
	runner.Run(ctx, func(err error) {
		log.Printf("heartbeat error: %v", err)
	})
	log.Println("vpspulse-agent stopped")
}
