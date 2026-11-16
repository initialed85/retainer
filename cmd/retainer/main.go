package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/initialed85/retainer/pkg/laya"
	"github.com/initialed85/retainer/pkg/memory"
	"github.com/initialed85/retainer/pkg/proxy"
)

type config struct {
	Addr         string
	Upstream     string
	Token        string
	SessionID    string
	FrontDoorKey string

	LayaURL    string
	LayaAPIKey string
	LayaModel  string

	TermsURL   string
	TermsModel string

	StoreURL string
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadConfig() config {
	return config{
		Addr:         envOr("ADDR", "0.0.0.0:8088"),
		Upstream:     envOr("UPSTREAM", "https://rooter.dev.initialed85.cc"),
		Token:        os.Getenv("TOKEN"),
		SessionID:    os.Getenv("SESSION_ID"),
		FrontDoorKey: os.Getenv("FRONT_DOOR_KEY"),

		LayaURL:    envOr("LAYA_URL", "https://laya-router.dev.initialed85.cc"),
		LayaAPIKey: os.Getenv("LAYA_API_KEY"),
		LayaModel:  envOr("LAYA_MODEL", "Laya-Q8_0"),

		TermsURL:   envOr("TERMS_URL", "https://sentiment-analyzer.dev.initialed85.cc/v1"),
		TermsModel: os.Getenv("TERMS_MODEL"),

		StoreURL: envOr("STORE_URL", "https://graph.dev.initialed85.cc/graphiti/search"),
	}
}

func main() {
	cfg := loadConfig()
	logger := log.New(os.Stderr, "experiment: ", log.LstdFlags)

	if cfg.Token == "" {
		logger.Fatal("TOKEN is required")
	}
	if cfg.FrontDoorKey == "" {
		logger.Printf("warning: FRONT_DOOR_KEY is unset - the proxy is open to anyone who can reach it")
	}

	sharedClient := &http.Client{
		Transport: &http.Transport{
			// The internal services use self-signed certificates.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Minute,
	}

	var decider memory.Decider
	if cfg.LayaAPIKey != "" {
		decider = laya.New(cfg.LayaURL, cfg.LayaAPIKey, cfg.LayaModel, sharedClient)
	}

	pipeline := memory.NewPipeline(
		decider,
		memory.NewSearchTermClient(cfg.TermsURL, cfg.TermsModel, sharedClient),
		memory.NewGraphitiClient(cfg.StoreURL, 10, sharedClient),
		logger,
	)

	handler := &proxy.Handler{
		Pipeline:     pipeline,
		Upstream:     cfg.Upstream,
		Token:        cfg.Token,
		SessionID:    cfg.SessionID,
		FrontDoorKey: cfg.FrontDoorKey,
		HTTP:         sharedClient,
		Logger:       logger,
	}

	server := &http.Server{Addr: cfg.Addr, Handler: handler}

	go func() {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
		<-interrupt
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	logger.Printf("listening on %s (upstream %s, session %q)", cfg.Addr, cfg.Upstream, cfg.SessionID)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal(err)
	}
}
