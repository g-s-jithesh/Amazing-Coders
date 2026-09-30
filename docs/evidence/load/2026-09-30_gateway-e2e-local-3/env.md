# Environment

- Captured (UTC): 2026-09-30T05:39:16+00:00
- Git: `7f0efd8913ade9ef57705c6036cb435dc67fe786` on `main` **(dirty working tree)**
- Host: Windows 11 / AMD64 / Intel64 Family 6 Model 154 Stepping 3, GenuineIntel / 12 logical CPUs / 15.7 GB RAM
- Docker: {'server_version': '29.8.0', 'ncpu': 12, 'mem_gb': 13.8, 'os': 'Docker Desktop (containerized)'}
- Kubernetes context: minikube

## Run configuration (fill in)

- Vehicles / rate Hz / simulator mode: 100,000 / 0.1 Hz / native (oem_a,oem_c MQTT; oem_b HTTPS), realistic noise, 120 s real time
- Kafka brokers / partitions / replication: 1 broker (KRaft, Docker Desktop, fresh after make down/up) / 6 canonical / RF 1
- Service replicas and CPU/memory limits: 1 ingest-gateway container (4 MQTT conns), no limits; Mosquitto 2.0.20; Redis 8
- Dataset size: master data seed 42 (100K vehicles); code at 7f0efd8
