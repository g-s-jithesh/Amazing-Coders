// Command stream-processor is the real-time brain (services/stream-processor/CLAUDE.md).
// Configuration is environment-only. `stream-processor healthcheck` probes /healthz (distroless image).
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/adapters/kafka"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/adapters/metrics"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/adapters/redis"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/adapters/scylla"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/rules"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadRules(rulesPath, dtcPath string) (rules.Config, error) {
	var cfg rules.Config
	b, err := os.ReadFile(rulesPath)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", rulesPath, err)
	}
	f, err := os.Open(dtcPath)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) < 2 {
		return cfg, fmt.Errorf("%s: %v", dtcPath, err)
	}
	cfg.DTCSeverity = map[string]string{}
	for _, r := range rows[1:] { // code,system,severity,...
		cfg.DTCSeverity[r[0]] = strings.ToLower(r[2])
	}
	return cfg, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	addr := env("SP_HTTP_ADDR", ":9102")
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := http.Get("http://127.0.0.1" + addr + "/healthz")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(addr); err != nil {
		slog.Error("stream-processor failed", "err", err)
		os.Exit(1)
	}
}

func run(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadRules(env("SP_RULES_FILE", "config/rules.yaml"), env("SP_DTC_CATALOGUE", "data/reference/dtc_catalogue.csv"))
	if err != nil {
		return err
	}
	brokers := strings.Split(env("KAFKA_BOOTSTRAP", "localhost:9092"), ",")

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	prom := metrics.New(reg)

	// SP_ROLE: processor (rules, alerts, sessions, rollups, state, live map), raw-sink (Scylla
	// telemetry_raw) or all. The roles use separate consumer groups, so a slow raw store can never
	// delay an alert; in K8s they are separate deployments that scale independently.
	role := env("SP_ROLE", "all")
	runProc, runRaw := role == "all" || role == "processor", role == "all" || role == "raw-sink"
	if !runProc && !runRaw {
		return fmt.Errorf("SP_ROLE %q, want processor | raw-sink | all", role)
	}
	var ready []func(context.Context) error

	var cons *kafka.Consumer
	if runProc {
		registry, err := kafka.FollowRegistry(ctx, brokers)
		if err != nil {
			return err
		}
		live, err := redis.New(env("REDIS_URL", "redis://localhost:6379/0"))
		if err != nil {
			return err
		}
		defer live.Close()
		emit, err := kafka.NewEmitter(brokers)
		if err != nil {
			return err
		}
		defer emit.Close()
		ready = append(ready, func(context.Context) error {
			if !registry.CaughtUp() {
				return errors.New("registry not caught up")
			}
			return nil
		})
		runner := &app.Runner{Engine: rules.New(cfg), Registry: registry, Emit: emit, Live: live, Obs: prom}
		if cons, err = kafka.NewConsumer(brokers, runner, prom); err != nil {
			return err
		}
		// Wait (bounded) for the registry so the first events already have tenant/fleet/duty context.
		go func() {
			deadline := time.Now().Add(60 * time.Second)
			for !registry.CaughtUp() && time.Now().Before(deadline) && ctx.Err() == nil {
				time.Sleep(200 * time.Millisecond)
			}
			slog.Info("registry loaded", "vehicles", registry.Len(), "caught_up", registry.CaughtUp())
		}()
	}
	var rawCons *kafka.RawConsumer
	if runRaw {
		store, err := scylla.New(env("SCYLLA_HOSTS", "localhost:9042"), 64)
		if err != nil {
			return fmt.Errorf("scylla: %w", err)
		}
		defer store.Close()
		ready = append(ready, store.Ping)
		if rawCons, err = kafka.NewRawConsumer(brokers, &app.RawSink{Store: store, Obs: prom}, prom); err != nil {
			return err
		}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, f := range ready {
			if err := f(c); err != nil {
				http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ready"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "err", err)
		}
	}()

	slog.Info("stream-processor started", "http", addr, "role", role)
	errs := make(chan error, 2)
	if cons != nil {
		go func() { errs <- cons.Run(ctx, 30*time.Second, 10*time.Second) }()
	}
	if rawCons != nil {
		go func() { errs <- rawCons.Run(ctx) }()
	}
	err = <-errs // the first role to stop ends the process (the orchestrator restarts it)
	stop()
	slog.Info("shutting down")
	if cons != nil {
		cons.Close() // leaves the group (revoke → flush of open windows)
	}
	if rawCons != nil {
		rawCons.Close()
	}
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
