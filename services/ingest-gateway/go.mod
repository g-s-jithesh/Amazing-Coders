module github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway

go 1.24.9

require (
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/g-s-jithesh/Amazing-Coders/libs/go-common v0.0.0
	github.com/g-s-jithesh/Amazing-Coders/libs/proto v0.0.0
	github.com/prometheus/client_golang v1.22.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/twmb/franz-go v1.20.7
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.18.4 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.12.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.44.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
)

replace github.com/g-s-jithesh/Amazing-Coders/libs/go-common => ../../libs/go-common

replace github.com/g-s-jithesh/Amazing-Coders/libs/proto => ../../libs/proto
