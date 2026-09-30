// Command ingest-gateway is the telemetry front door (services/ingest-gateway/CLAUDE.md).
// Configuration is environment-only (12-factor); see .env.example. `ingest-gateway healthcheck`
// probes /healthz for container health checks (the image has no shell or curl).
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/http"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/kafka"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/metrics"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/mqtt"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/oem"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/adapters/redis"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/pipeline"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

// parseKeys reads GATEWAY_HMAC_KEYS = "id=secret:oem_a|oem_b;id2=secret2:oem_c".
func parseKeys(s string) (map[string]http.Key, error) {
	out := map[string]http.Key{}
	for _, item := range strings.Split(s, ";") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		id, rest, ok1 := strings.Cut(item, "=")
		secret, oems, ok2 := strings.Cut(rest, ":")
		if !ok1 || !ok2 || id == "" || secret == "" {
			return nil, fmt.Errorf("GATEWAY_HMAC_KEYS entry %q, want id=secret:oem|oem", item)
		}
		k := http.Key{Secret: []byte(secret), OEMs: map[string]bool{}}
		for _, o := range strings.Split(oems, "|") {
			k.OEMs[o] = true
		}
		out[id] = k
	}
	return out, nil
}

func loadWMI(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) < 2 {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	m := map[string]string{}
	for _, r := range rows[1:] {
		m[r[0]] = r[1]
	}
	return m, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := nethttp.Get("http://127.0.0.1" + env("GATEWAY_HTTP_ADDR", ":8081") + "/healthz")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("ingest-gateway failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	keys, err := parseKeys(env("GATEWAY_HMAC_KEYS", "sim=dev-only-hmac-secret:oem_a|oem_b|oem_c"))
	if err != nil {
		return err
	}
	wmi, err := loadWMI(env("GATEWAY_WMI_FILE", "data/reference/wmi_synthetic.csv"))
	if err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	obs := metrics.New(reg)

	prod, err := kafka.NewProducer(strings.Split(env("KAFKA_BOOTSTRAP", "localhost:9092"), ","), envInt("GATEWAY_MAX_BUFFERED", 200_000))
	if err != nil {
		return err
	}

	decoders := map[string]app.Decoder{}
	for id, d := range oem.Registry {
		decoders[id] = d
	}
	svcCfg := &app.Service{Decoders: decoders, Chain: pipeline.Default(wmi), Encoder: kafka.ProtoEncoder{}, Producer: prod, Observer: obs}
	var conf *redis.Confirmer
	if url := env("REDIS_URL", "redis://localhost:6379/0"); url != "none" {
		if conf, err = redis.New(url, 50*time.Millisecond); err != nil {
			return err
		}
		svcCfg.Confirmer = conf
	}
	svc := app.NewService(envInt("GATEWAY_DEDUP_EPS", 20_000), svcCfg)

	srv := &http.Server{
		Svc: svc, Keys: keys, Backlog: prod.Backlog, HighWater: int64(envInt("GATEWAY_BACKLOG_HIGH", 150_000)),
		Ready:   prod.Ping,
		Metrics: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		Backpressure: func(on bool) {
			if on {
				obs.Backpressure.Set(1)
			} else {
				obs.Backpressure.Set(0)
			}
		},
		Now: time.Now,
	}
	httpSrv := &nethttp.Server{Addr: env("GATEWAY_HTTP_ADDR", ":8081"), Handler: srv.Routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errc <- err
		}
	}()

	var cons *mqtt.Consumer
	if url := env("GATEWAY_MQTT_URL", "tcp://localhost:1883"); url != "none" {
		// Stable client IDs (e.g. a StatefulSet pod name): a restarted replica must reclaim its persistent
		// session and drain what the broker queued for it. A new ID per restart would leave the old
		// session in the shared group, still receiving (and parking) its share of messages.
		cons, err = mqtt.Start(ctx, mqtt.Config{URL: url, ClientID: env("GATEWAY_MQTT_CLIENT_ID", "ingest-gateway-0"), Conns: envInt("GATEWAY_MQTT_CONNS", 4)}, svc, obs.MQTTUnacked.Add)
		if err != nil {
			return err
		}
	}
	slog.Info("ingest-gateway started", "http", httpSrv.Addr, "mqtt", cons != nil, "dedup_redis", conf != nil)

	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	slog.Info("shutting down")
	if cons != nil {
		cons.Close() // stop intake first
	}
	shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shut)
	if cerr := prod.Close(shut); cerr != nil && err == nil {
		err = cerr
	}
	if conf != nil {
		_ = conf.Close()
	}
	return err
}
