// Package mqtt publishes OEM payloads to the broker at QoS 1 over a small pool of connections.
// A VIN always maps to the same connection, so per-vehicle order is kept. At most MaxInflight
// unacknowledged messages exist per connection; beyond that Publish blocks (back-pressure).
//
// ponytail: plain TCP for now; OEM-cloud mTLS certs (CN = oem id) arrive with infra/pki (F-10).
package mqtt

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Topic is the wire topic per OEM (CLAUDE.md §4.1).
func Topic(oem, vin string) (string, error) {
	switch oem {
	case "oem_a":
		return "v1/oem_a/" + vin + "/telemetry", nil
	case "oem_c":
		return "v1/oem_c/" + vin + "/t", nil
	}
	return "", fmt.Errorf("mqtt: oem %q does not use MQTT", oem)
}

type Config struct {
	BrokerURL   string // tcp://localhost:1883
	ClientID    string // prefix; connection i gets <prefix>-<i>
	Conns       int
	MaxInflight int
}

type Publisher struct {
	clients []paho.Client
	sems    []chan struct{}
	wg      sync.WaitGroup
	errMu   sync.Mutex
	lastErr error
}

func New(cfg Config) (*Publisher, error) {
	if cfg.Conns < 1 {
		cfg.Conns = 1
	}
	if cfg.MaxInflight < 1 {
		cfg.MaxInflight = 1000
	}
	p := &Publisher{}
	for i := 0; i < cfg.Conns; i++ {
		opts := paho.NewClientOptions().
			AddBroker(cfg.BrokerURL).
			SetClientID(fmt.Sprintf("%s-%d", cfg.ClientID, i)).
			SetOrderMatters(false). // ordering is per connection anyway; don't serialise callbacks
			SetAutoReconnect(true).
			SetConnectRetry(true).
			SetConnectTimeout(10 * time.Second).
			SetWriteTimeout(10 * time.Second)
		c := paho.NewClient(opts)
		if tok := c.Connect(); !tok.WaitTimeout(15*time.Second) || tok.Error() != nil {
			p.Close()
			return nil, fmt.Errorf("mqtt: connect %s: %v", cfg.BrokerURL, tok.Error())
		}
		p.clients = append(p.clients, c)
		p.sems = append(p.sems, make(chan struct{}, cfg.MaxInflight))
	}
	return p, nil
}

func (p *Publisher) Publish(ctx context.Context, oem, vin string, payload []byte) error {
	topic, err := Topic(oem, vin)
	if err != nil {
		return err
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(vin))
	i := int(h.Sum32() % uint32(len(p.clients)))
	select {
	case p.sems[i] <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	tok := p.clients[i].Publish(topic, 1, false, payload)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		tok.Wait()
		<-p.sems[i]
		if err := tok.Error(); err != nil {
			p.errMu.Lock()
			p.lastErr = err
			p.errMu.Unlock()
		}
	}()
	return p.takeErr()
}

// takeErr reports (once) the last asynchronous PUBACK failure.
func (p *Publisher) takeErr() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	err := p.lastErr
	p.lastErr = nil
	return err
}

// Close waits for in-flight PUBACKs, then disconnects.
func (p *Publisher) Close() error {
	p.wg.Wait()
	for _, c := range p.clients {
		c.Disconnect(2000)
	}
	return p.takeErr()
}
