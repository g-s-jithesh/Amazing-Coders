//go:build integration

package mqtt

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Needs a broker: `make up`, then go test -tags integration ./... (MQTT_URL overrides tcp://localhost:1883).
func TestPublishReachesSubscriber(t *testing.T) {
	url := os.Getenv("MQTT_URL")
	if url == "" {
		url = "tcp://localhost:1883"
	}
	var got atomic.Int64
	sub := paho.NewClient(paho.NewClientOptions().AddBroker(url).SetClientID(fmt.Sprintf("it-sub-%d", time.Now().UnixNano())))
	if tok := sub.Connect(); !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		t.Skipf("no broker at %s: %v", url, tok.Error())
	}
	defer sub.Disconnect(100)
	sub.Subscribe("v1/+/ITVIN0000000000/#", 1, func(paho.Client, paho.Message) { got.Add(1) }).Wait()

	p, err := New(Config{BrokerURL: url, ClientID: fmt.Sprintf("it-pub-%d", time.Now().UnixNano()), Conns: 2, MaxInflight: 50})
	if err != nil {
		t.Fatal(err)
	}
	const n = 2000
	for i := 0; i < n; i++ {
		oem := []string{"oem_a", "oem_c"}[i%2]
		if err := p.Publish(context.Background(), oem, "ITVIN0000000000", []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for got.Load() < n && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got.Load() != n {
		t.Fatalf("subscriber got %d of %d", got.Load(), n)
	}
}
