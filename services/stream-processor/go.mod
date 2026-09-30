module github.com/g-s-jithesh/Amazing-Coders/services/stream-processor

go 1.24.9

require (
	github.com/apache/cassandra-gocql-driver/v2 v2.1.2
	github.com/g-s-jithesh/Amazing-Coders/libs/go-common v0.0.0-00010101000000-000000000000
	github.com/g-s-jithesh/Amazing-Coders/libs/proto v0.0.0-00010101000000-000000000000
	github.com/prometheus/client_golang v1.22.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/twmb/franz-go v1.20.7
	github.com/twmb/franz-go/pkg/kadm v1.16.1
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/klauspost/compress v1.18.4 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.12.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
)

replace github.com/g-s-jithesh/Amazing-Coders/libs/proto => ../../libs/proto

replace github.com/g-s-jithesh/Amazing-Coders/libs/go-common => ../../libs/go-common
