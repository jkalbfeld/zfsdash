package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jkalbfeld/zfsdash/internal/alerts"
	"github.com/jkalbfeld/zfsdash/internal/config"
	"github.com/jkalbfeld/zfsdash/internal/store"
	"github.com/jkalbfeld/zfsdash/internal/web"
	"github.com/jkalbfeld/zfsdash/internal/zfs"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Build collector
	var collector zfs.Collector
	switch cfg.Mode {
	case "truenas":
		collector = zfs.NewTrueNASCollector(&cfg.TrueNAS)
	default:
		collector = zfs.NewSSHCollector(&cfg.SSH)
	}

	// In-memory store
	st := store.New()

	// Alert manager
	am := alerts.New(cfg)

	// Initial collection
	log.Printf("zfsdash starting — collector: %s", collector.Name())
	if data, err := collector.Collect(); err != nil {
		log.Printf("initial collection error: %v", err)
	} else {
		st.Set(data)
		am.Evaluate(data)
	}

	// Background poller
	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	go func() {
		for range ticker.C {
			data, err := collector.Collect()
			if err != nil {
				log.Printf("collection error: %v", err)
				continue
			}
			st.Set(data)
			am.Evaluate(data)
		}
	}()

	// HTTP server
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	h := web.NewHandler(st, cfg)
	h.Register(r)

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: r,
	}

	go func() {
		log.Printf("listening on %s", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ticker.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("shutdown complete")
}
