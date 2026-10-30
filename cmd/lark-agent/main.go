package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		telemetry.Error(ctx, "agent.process.failed", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("lark-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.String("mode", "agent", "agent, worker, models or health")
	path := flags.String("config", "/etc/agent/agent.toml", "independent configuration path")
	listen := flags.String("listen", "", "HTTP listen address")
	healthURL := flags.String("health-url", "http://127.0.0.1:8080/health/ready", "local readiness probe")
	check := flags.Bool("check", false, "validate configuration without starting receivers or making external calls")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("invalid command arguments")
	}
	if *mode == "health" {
		return health(ctx, *healthURL)
	}
	if *mode != "agent" && *mode != "worker" && *mode != "models" {
		return errors.New("unknown agent mode")
	}
	c, err := config.Load(*path, *mode)
	if err != nil {
		return err
	}
	if *check {
		return nil
	}
	shutdown, err := telemetry.Initialize(ctx, *mode)
	if err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		telemetry.Info(stop, "agent.process.stopping", "mode", *mode)
		if err := shutdown(stop); err != nil {
			telemetry.Error(stop, "agent.telemetry.shutdown_failed", err)
		}
	}()
	telemetry.Info(ctx, "agent.process.starting", "mode", *mode, "transport", c.Lark.Transport, "max_concurrent_tasks", c.Runtime.MaxConcurrentTasks, "context_limit_bytes", c.Runtime.MaxContextBytes)
	if *listen == "" {
		if *mode == "models" {
			*listen = ":8081"
		} else {
			*listen = ":8080"
		}
	}
	switch *mode {
	case "models":
		return models(ctx, c, *listen)
	case "worker":
		return processing(ctx, c, *listen)
	default:
		return participating(ctx, c, *listen)
	}
}
func endpoint(c config.Endpoint) modelapi.Endpoint {
	return modelapi.Endpoint{BaseURL: c.BaseURL, APIKey: c.APIKey, Model: c.Model, Protocol: c.Protocol, ServiceTier: c.ServiceTier, Timeout: time.Duration(c.TimeoutSeconds) * time.Second}
}
func models(ctx context.Context, c config.Config, listen string) error {
	e, err := modelapi.NewEmbedder(modelapi.EmbeddingConfig{Endpoint: endpoint(c.Models.Embedding.Endpoint), Dimensions: c.Models.Embedding.Dimensions, Concurrency: c.Models.Embedding.Concurrency, MaxInputs: c.Models.Embedding.MaxInputs})
	if err != nil {
		return errors.New("embedding adapter initialization failed")
	}
	r, err := modelapi.NewReranker(endpoint(c.Models.Rerank))
	if err != nil {
		return errors.New("reranking adapter initialization failed")
	}
	h, err := modelapi.NewGateway(modelapi.GatewayConfig{APIKey: c.Gateway.APIKey, EmbeddingModel: c.Gateway.EmbeddingModel, RerankModel: c.Gateway.RerankModel, Dimensions: c.Models.Embedding.Dimensions, MaxInputs: c.Models.Embedding.MaxInputs, MaxBodyBytes: c.Gateway.MaxBodyBytes, Timeout: time.Duration(c.Gateway.TimeoutSeconds) * time.Second}, e, r)
	if err != nil {
		return errors.New("model gateway initialization failed")
	}
	return serve(ctx, listen, h)
}
func serve(ctx context.Context, address string, h http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("HTTP listener startup failed")
	}
	s := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 310 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- s.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("HTTP server stopped unexpectedly")
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(stop); err != nil {
			s.Close()
		}
		<-done
		return nil
	}
}
func health(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "localhost" && net.ParseIP(u.Hostname()) == nil) {
		return errors.New("invalid local health probe URL")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !ip.IsLoopback() {
		return errors.New("health probe requires a loopback address")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return errors.New("invalid health probe")
	}
	client := http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("service health probe failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("service is not ready")
	}
	return nil
}
