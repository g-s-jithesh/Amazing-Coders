// Package mqtt consumes vehicle/OEM telemetry from the broker with a shared subscription
// ($share/ingest/v1/+/+/#, QoS 1) over several connections, so gateway replicas and connections
// split the load. Acks are manual: a message is acknowledged only after Kafka acknowledged its
// outputs. While Kafka is down the handler keeps retrying and does not ack, so the broker's
// in-flight window fills and it stops sending (back-pressure without loss).
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/app"
)

const Filter = "$share/ingest/v1/+/+/#"

type Config struct {
	URL      string
	ClientID string
	Conns    int
}

type Consumer struct{ clients []paho.Client }

// ParseTopic extracts the OEM and VIN from v1/{oem}/{vin}/...; ok=false for anything else.
func ParseTopic(topic string) (oem, vin string, ok bool) {
	p := strings.Split(topic, "/")
	if len(p) < 4 || p[0] != "v1" || p[1] == "" || p[2] == "" {
		return "", "", false
	}
	return p[1], p[2], true
}

// Start connects and subscribes. unacked tracks messages held without an ack.
func Start(ctx context.Context, cfg Config, svc *app.Service, unacked func(delta float64)) (*Consumer, error) {
	c := &Consumer{}
	handle := func(_ paho.Client, m paho.Message) {
		oem, vin, ok := ParseTopic(m.Topic())
		if !ok {
			oem = "unknown-topic:" + m.Topic() // → UNKNOWN_OEM in the DLQ
		}
		unacked(1)
		defer unacked(-1)
		backoff := 100 * time.Millisecond
		for {
			_, err := svc.Handle(ctx, app.Message{OEM: oem, TopicVIN: vin, Body: m.Payload()})
			if err == nil {
				m.Ack()
				return
			}
			if ctx.Err() != nil {
				return // shutting down: leave unacked, the broker redelivers to another replica
			}
			slog.Warn("kafka unavailable; holding MQTT message", "err", err, "retry_in", backoff)
			time.Sleep(backoff)
			backoff = min(2*backoff, 5*time.Second)
		}
	}
	for i := 0; i < max(1, cfg.Conns); i++ {
		opts := paho.NewClientOptions().AddBroker(cfg.URL).
			SetClientID(fmt.Sprintf("%s-%d", cfg.ClientID, i)).
			SetCleanSession(false). // keep the subscription + unacked QoS 1 messages across reconnects
			SetAutoAckDisabled(true).
			SetOrderMatters(false).
			SetAutoReconnect(true).
			SetConnectRetry(true).
			SetOnConnectHandler(func(cl paho.Client) {
				if tok := cl.Subscribe(Filter, 1, handle); tok.Wait() && tok.Error() != nil {
					slog.Error("mqtt subscribe failed", "err", tok.Error())
				}
			})
		cl := paho.NewClient(opts)
		if tok := cl.Connect(); !tok.WaitTimeout(15*time.Second) || tok.Error() != nil {
			c.Close()
			return nil, fmt.Errorf("mqtt connect %s: %v", cfg.URL, tok.Error())
		}
		c.clients = append(c.clients, cl)
	}
	return c, nil
}

func (c *Consumer) Close() {
	for _, cl := range c.clients {
		cl.Disconnect(2000)
	}
}
