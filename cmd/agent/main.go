package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/darkerline/agent/internal/api"
	"github.com/darkerline/agent/internal/config"
	"github.com/darkerline/agent/internal/xray"
)

func main() {
	cfgPath := flag.String("config", "/etc/darkline-agent/config.json", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.AgentToken == "" {
		log.Println("WARNING: AGENT_TOKEN not set — running without auth!")
	}

	manager := xray.NewManager(cfg.XrayConfig)
	process := xray.NewProcess(cfg)

	// Start Xray if binary exists
	if _, err := os.Stat(cfg.XrayBin); err == nil {
		log.Printf("Starting Xray: %s", cfg.XrayBin)
		if err := process.Start(); err != nil {
			log.Printf("Xray start failed: %v (will retry on reload)", err)
		}
	} else {
		log.Printf("Xray binary not found at %s — agent running without Xray process management", cfg.XrayBin)
	}

	// HTTP API server
	srv := api.New(cfg, manager, process)

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Println("Shutting down...")
		process.Stop()
		os.Exit(0)
	}()

	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
