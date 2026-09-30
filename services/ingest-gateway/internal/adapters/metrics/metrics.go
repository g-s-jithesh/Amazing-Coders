// Package metrics implements app.Observer with Prometheus (names from services/ingest-gateway/CLAUDE.md).
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

type Prom struct {
	Events       *prometheus.CounterVec
	DLQ          *prometheus.CounterVec
	DedupHits    prometheus.Counter
	DedupDeg     prometheus.Counter
	ProduceLat   prometheus.Histogram
	Backpressure prometheus.Gauge
	MQTTUnacked  prometheus.Gauge
}

func New(reg prometheus.Registerer) *Prom {
	p := &Prom{
		Events:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ingest_events_total", Help: "Records by OEM and result (accepted | rejected | duplicate)."}, []string{"oem", "result"}),
		DLQ:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "dlq_total", Help: "Records sent to telemetry.dlq.v1 by reason."}, []string{"reason"}),
		DedupHits:    prometheus.NewCounter(prometheus.CounterOpts{Name: "dedup_hits_total", Help: "Duplicates dropped after Redis confirmation."}),
		DedupDeg:     prometheus.NewCounter(prometheus.CounterOpts{Name: "dedup_degraded_total", Help: "Messages whose Bloom hits could not be confirmed (Redis down); kept."}),
		ProduceLat:   prometheus.NewHistogram(prometheus.HistogramOpts{Name: "produce_latency_seconds", Help: "Kafka produce until all acks, per message.", Buckets: prometheus.ExponentialBuckets(0.001, 2, 14)}),
		Backpressure: prometheus.NewGauge(prometheus.GaugeOpts{Name: "backpressure_active", Help: "1 while the producer backlog is above the high watermark."}),
		MQTTUnacked:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "mqtt_unacked", Help: "MQTT messages received but not yet acknowledged."}),
	}
	reg.MustRegister(p.Events, p.DLQ, p.DedupHits, p.DedupDeg, p.ProduceLat, p.Backpressure, p.MQTTUnacked)
	for _, r := range canonical.Reasons {
		p.DLQ.WithLabelValues(string(r)) // export zeros so dashboards show every reason
	}
	return p
}

func (p *Prom) Accepted(oem string, n int) { p.Events.WithLabelValues(oem, "accepted").Add(float64(n)) }
func (p *Prom) Rejected(oem string, r canonical.Reason, n int) {
	p.Events.WithLabelValues(oem, "rejected").Add(float64(n))
	p.DLQ.WithLabelValues(string(r)).Add(float64(n))
}
func (p *Prom) Duplicates(n int) {
	p.DedupHits.Add(float64(n))
	p.Events.WithLabelValues("", "duplicate").Add(float64(n))
}
func (p *Prom) DedupDegraded()                 { p.DedupDeg.Inc() }
func (p *Prom) ProduceLatency(d time.Duration) { p.ProduceLat.Observe(d.Seconds()) }
