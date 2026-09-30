// Command alerts-tail prints alerts.v1 as JSON lines (one per alert, with the Kafka append time),
// for demos and latency evidence.
//
//	go run ./cmd/alerts-tail --from start --vin 0KCD... --for 30s
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	alertsv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/alerts/v1"
)

func main() {
	brokers := flag.String("brokers", "localhost:9092", "Kafka bootstrap")
	from := flag.String("from", "end", "start | end")
	vin := flag.String("vin", "", "only this VIN")
	dur := flag.Duration("for", 0, "stop after this long (0 = until Ctrl-C)")
	flag.Parse()

	off := kgo.NewOffset().AtEnd()
	if *from == "start" {
		off = kgo.NewOffset().AtStart()
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(*brokers, ",")...), kgo.ConsumeTopics("alerts.v1"), kgo.ConsumeResetOffset(off))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer cl.Close()
	ctx := context.Background()
	if *dur > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *dur)
		defer cancel()
	}
	for ctx.Err() == nil {
		cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			if *vin != "" && string(r.Key) != *vin {
				return
			}
			var a alertsv1.Alert
			if proto.Unmarshal(r.Value, &a) != nil {
				return
			}
			b, _ := protojson.Marshal(&a)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			m["kafkaAppendMs"] = r.Timestamp.UnixMilli()
			out, _ := json.Marshal(m)
			fmt.Println(string(out))
		})
	}
}
