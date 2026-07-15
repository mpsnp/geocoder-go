package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/GameTec-live/geocoder-go/internal/api"
	"github.com/GameTec-live/geocoder-go/internal/geocoder"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func execute(arguments []string) error {
	if len(arguments) > 0 {
		switch arguments[0] {
		case "healthcheck":
			return healthcheck(arguments[1:])
		case "version":
			fmt.Printf("geocoder %s (commit %s, built %s)\n", version, commit, buildDate)
			return nil
		}
	}
	return run(arguments)
}

func run(arguments []string) error {
	dataDefault := environment("GEOCODER_DATA", "data")
	listenDefault := environment("GEOCODER_LISTEN", ":8080")
	flags := flag.NewFlagSet("geocoder", flag.ContinueOnError)
	data := flags.String("data", dataDefault, "directory recursively containing SQLite packs")
	listen := flags.String("listen", listenDefault, "HTTP listen address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	ctx := context.Background()
	service, err := geocoder.Open(ctx, *data)
	if err != nil {
		return err
	}
	defer func() { _ = service.Close() }()
	if len(service.PackNames()) == 0 {
		return fmt.Errorf("no geocoder packs found below %s; generate one with packgen", *data)
	}

	server := &http.Server{
		Addr:              *listen,
		Handler:           api.Router(service),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-shutdownSignal()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("geocoder listening", "address", *listen, "packs", service.PackNames(), "version", version, "commit", commit)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthcheck(arguments []string) error {
	endpoint := environment("GEOCODER_HEALTHCHECK_URL", "http://127.0.0.1:8080/healthz")
	if len(arguments) > 1 {
		return fmt.Errorf("healthcheck accepts at most one URL")
	}
	if len(arguments) == 1 {
		endpoint = arguments[0]
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned %s", response.Status)
	}
	return nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func shutdownSignal() <-chan os.Signal {
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM)
	return channel
}
