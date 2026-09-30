// Package metrics exports the stream-processor's Prometheus metrics (app.Observer + consumer hooks).
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/app"
)

type Prom struct {
	events, late, lateRules, unknown, decode, retries prometheus.Counter
	alerts                                            *prometheus.CounterVec
	sinkErr                                           *prometheus.CounterVec
	sessions, rollups                                 prometheus.Counter
	alertLatency, batchSec, commitSec                 prometheus.Histogram
	sinkSec                                           *prometheus.HistogramVec
}

func New(reg prometheus.Registerer) *Prom {
	c := func(name, help string) prometheus.Counter {
		return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	}
	p := &Prom{
		events:    c("sp_events_total", "Canonical events processed."),
		late:      c("late_events_total", "Events beyond the 30 s lateness for rollups (still stored)."),
		lateRules: c("late_for_rules_total", "Events older than the vehicle's newest event (stored, not rule-evaluated)."),
		unknown:   c("unregistered_vin_events_total", "Events for VINs not (yet) in fleet.vehicle.v1."),
		decode:    c("decode_errors_total", "Canonical records that failed to unmarshal."),
		retries:   c("sink_flush_retries_total", "Flush attempts that failed and were retried (back-pressure)."),
		sessions:  c("sessions_total", "Session records emitted."),
		rollups:   c("rollups_total", "1-minute rollups emitted."),
		alerts:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "alerts_total", Help: "Alert transitions."}, []string{"rule", "severity", "state"}),
		sinkErr:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "sink_errors_total", Help: "Sink write failures."}, []string{"sink"}),
		alertLatency: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "alert_latency_seconds",
			Help:    "Gateway receive of the triggering event → alert emitted (FIRING, event-driven rules).",
			Buckets: []float64{.05, .1, .25, .5, 1, 2, 3, 5, 10, 30}}),
		batchSec:  prometheus.NewHistogram(prometheus.HistogramOpts{Name: "batch_processing_seconds", Help: "Processing time per poll batch (excl. sinks).", Buckets: prometheus.ExponentialBuckets(0.0005, 2, 14)}),
		commitSec: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "commit_seconds", Help: "Offset commit latency.", Buckets: prometheus.ExponentialBuckets(0.001, 2, 12)}),
		sinkSec: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "sink_write_seconds", Help: "Per-batch sink write time incl. retries.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15)}, []string{"sink"}),
	}
	reg.MustRegister(p.events, p.late, p.lateRules, p.unknown, p.decode, p.retries, p.sessions, p.rollups, p.alerts, p.sinkErr, p.alertLatency, p.batchSec, p.commitSec, p.sinkSec)
	return p
}

func (p *Prom) Batch(o *app.Outputs, d time.Duration) {
	p.events.Add(float64(len(o.Raw)))
	p.late.Add(float64(o.Late))
	p.lateRules.Add(float64(o.LateRule))
	p.unknown.Add(float64(o.Unknown))
	p.sessions.Add(float64(len(o.Sessions)))
	p.rollups.Add(float64(len(o.Rollups)))
	p.batchSec.Observe(d.Seconds())
	for _, a := range o.Alerts {
		p.alerts.WithLabelValues(a.RuleID, a.Severity, a.State).Inc()
		if a.State == "FIRING" && a.TriggerIngestMs > 0 {
			p.alertLatency.Observe(float64(a.ProcessedMs-a.TriggerIngestMs) / 1000)
		}
	}
}

func (p *Prom) SinkError(s string)                 { p.sinkErr.WithLabelValues(s).Inc() }
func (p *Prom) FlushRetry()                        { p.retries.Inc() }
func (p *Prom) SinkTime(s string, d time.Duration) { p.sinkSec.WithLabelValues(s).Observe(d.Seconds()) }
func (p *Prom) DecodeError()                       { p.decode.Inc() }
func (p *Prom) Commit(d time.Duration)             { p.commitSec.Observe(d.Seconds()) }
