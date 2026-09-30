package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/encoders"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/groundtruth"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/refdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/transport/https"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/transport/kafka"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/transport/mqtt"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/fault"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/noise"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// router sends each OEM over its native transport (CLAUDE.md §4.1): oem_a/oem_c → MQTT, oem_b → HTTPS.
type router struct {
	mq *mqtt.Publisher
	hs *https.Publisher
}

func (r router) Publish(ctx context.Context, oem, vin string, b []byte) error {
	if oem == "oem_b" {
		return r.hs.Publish(ctx, oem, vin, b)
	}
	return r.mq.Publish(ctx, oem, vin, b)
}

func (r router) Dropped() int64 { return r.hs.Dropped() }

func (r router) Close() error {
	e1, e2 := r.mq.Close(), r.hs.Close()
	if e1 != nil {
		return e1
	}
	return e2
}

func parseShard(s string) (int, int, error) {
	var i, n int
	if _, err := fmt.Sscanf(s, "%d/%d", &i, &n); err != nil || n < 1 || i < 0 || i >= n {
		return 0, 0, fmt.Errorf("--shard %q, want i/n with 0 ≤ i < n", s)
	}
	return i, n, nil
}

func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	seedV := fs.Uint64("seed", 42, "RNG seed (must match `make seed`)")
	vehicles := fs.Int("vehicles", 100_000, "fleet size (must match `make seed`)")
	tenants := fs.Int("tenants", 3, "tenants (must match `make seed`)")
	ref := fs.String("ref", "data/reference", "reference data directory")
	mode := fs.String("mode", "mqtt", "mqtt (native: a,c→MQTT, b→HTTPS) | https (all batches) | kafka-direct (load tests)")
	rate := fs.Float64("rate-hz", 0.1, "periodic reports per vehicle per sim second")
	dt := fs.Float64("dt", 1, "sim seconds per tick")
	speedup := fs.Float64("speedup", 1, "sim seconds per wall second (0 = as fast as possible)")
	startS := fs.String("start", "", "sim start RFC 3339 (default: now)")
	duration := fs.Duration("duration", 0, "sim time to run (0 = until Ctrl-C)")
	shardS := fs.String("shard", "0/1", "run vehicles with index % n == i")
	noiseS := fs.String("noise", "realistic", "clean | realistic | hostile")
	faultRate := fs.Float64("fault-rate", 0.5, "random fault onsets per vehicle-year at SoH 100 %")
	gtOut := fs.String("ground-truth-out", "data/ground_truth", "ground-truth Parquet dir (\"\" disables)")
	var injects multiFlag
	fs.Var(&injects, "demo-inject", "<vin>:<fault> started at sim start (repeatable)")
	demoPrecursor := fs.Duration("demo-precursor", 12*time.Hour, "precursor for --demo-inject (≥ 12h looks right on camera)")
	workers := fs.Int("workers", runtime.NumCPU(), "worker goroutines")
	mqttURL := fs.String("mqtt-url", "tcp://localhost:1883", "MQTT broker")
	mqttConns := fs.Int("mqtt-conns", 4, "MQTT connections")
	httpsURL := fs.String("https-url", "http://localhost:8081", "ingest-gateway base URL")
	kafkaBrokers := fs.String("kafka-brokers", "localhost:9092", "comma-separated brokers (kafka-direct)")
	_ = fs.Parse(args)

	shardI, shardN, err := parseShard(*shardS)
	if err != nil {
		return err
	}
	prof, err := noise.ProfileByName(*noiseS)
	if err != nil {
		return err
	}
	start := time.Now().UTC().Truncate(time.Second)
	if *startS != "" {
		if start, err = time.Parse(time.RFC3339, *startS); err != nil {
			return err
		}
	}

	models, err := refdata.Models(*ref)
	if err != nil {
		return err
	}
	wmis, err := refdata.WMIs(*ref)
	if err != nil {
		return err
	}
	ds, err := masterdata.Generate(masterdata.Config{Seed: *seedV, Vehicles: *vehicles, Tenants: *tenants, Models: models, WMI: wmis})
	if err != nil {
		return err
	}
	mine := &masterdata.Dataset{Depots: ds.Depots}
	for i := range ds.Vehicles {
		if i%shardN == shardI {
			mine.Vehicles = append(mine.Vehicles, ds.Vehicles[i])
			mine.Duties = append(mine.Duties, ds.Duties[i])
		}
	}
	vs, err := app.BuildFleet(mine, models, *seedV, start, app.Options{FaultRatePerYear: *faultRate})
	if err != nil {
		return err
	}
	for _, spec := range injects {
		vin, k, err := fault.ParseInject(spec)
		if err != nil {
			return err
		}
		found := false
		for _, v := range vs {
			if v.VIN == vin {
				v.InjectFault(k, start.UnixMilli(), *demoPrecursor)
				found = true
			}
		}
		if !found {
			return fmt.Errorf("--demo-inject: %s is not in this shard's fleet", vin)
		}
		slog.Info("demo fault injected", "vin", vin, "fault", k, "dtc_at", start.Add(*demoPrecursor))
	}

	var pub app.Publisher
	secret := []byte(os.Getenv("SIM_HMAC_SECRET"))
	if len(secret) == 0 {
		secret = []byte("dev-only-hmac-secret")
	}
	newHTTPS := func() *https.Publisher {
		return https.New(https.Config{BaseURL: *httpsURL, KeyID: "sim", Secret: secret})
	}
	switch *mode {
	case "mqtt":
		mq, err := mqtt.New(mqtt.Config{BrokerURL: *mqttURL, ClientID: fmt.Sprintf("sim-%d-of-%d", shardI, shardN), Conns: *mqttConns})
		if err != nil {
			return err
		}
		pub = router{mq, newHTTPS()}
	case "https":
		pub = newHTTPS()
	case "kafka-direct":
		if pub, err = kafka.New(strings.Split(*kafkaBrokers, ",")); err != nil {
			return err
		}
	default:
		return fmt.Errorf("--mode %q (mqtt|https|kafka-direct; backfill arrives with the batch layer)", *mode)
	}

	var truth app.TruthSink
	if *gtOut != "" {
		w, err := groundtruth.Open(*gtOut, fmt.Sprintf("%s-shard%dof%d", start.Format("20060102T150405Z"), shardI, shardN))
		if err != nil {
			return err
		}
		defer w.Close()
		truth = w
	}

	encode := func(s *telemetry.Sample) ([]byte, error) { return encoders.ByOEM[s.OEM](s, nil) }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var st app.Stats
	go logStats(ctx, &st)

	slog.Info("simulator running", "vehicles", len(vs), "shard", *shardS, "mode", *mode, "rate_hz", *rate,
		"speedup", *speedup, "noise", *noiseS, "start", start, "target_eps", float64(len(vs))**rate**speedup)
	runErr := app.Run(ctx, vs, app.RunConfig{
		Seed: *seedV, Start: start, Dt: *dt, RateHz: *rate, Speedup: *speedup, Duration: *duration,
		Workers: *workers, Noise: prof, BurstFactor: 3, BurstFor: 5 * time.Minute,
	}, encode, pub, truth, &st)
	if err := pub.Close(); err != nil {
		slog.Warn("publisher closed with undelivered records", "err", err) // e.g. gateway not running yet
	}
	logLine(&st)
	if d, ok := pub.(interface{ Dropped() int64 }); ok {
		slog.Info("https", "dropped_records", d.Dropped())
	}
	return runErr
}

var statsStart = time.Now()

func logLine(st *app.Stats) {
	el := time.Since(statsStart).Seconds()
	slog.Info("stats", "sim_now", time.UnixMilli(st.SimNowMs.Load()).UTC().Format(time.RFC3339),
		"samples", st.Samples.Load(), "published", st.Published.Load(), "publish_errors", st.PublishErrors.Load(),
		"encode_errors", st.EncodeErrors.Load(), "avg_eps", int64(float64(st.Published.Load())/el))
}

func logStats(ctx context.Context, st *app.Stats) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			logLine(st)
		}
	}
}
