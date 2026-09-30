# Environment

- Captured (UTC): 2026-09-30T04:48:59+00:00
- Git: `652609a92e73639ad3970a19a75475cfca589561` on `main` **(dirty working tree)**
- Host: Windows 11 / AMD64 / Intel64 Family 6 Model 154 Stepping 3, GenuineIntel / 12 logical CPUs / 15.7 GB RAM
- Docker: {'server_version': '29.8.0', 'ncpu': 12, 'mem_gb': 13.8, 'os': 'Docker Desktop (containerized)'}
- Kubernetes context: minikube

## Run configuration (fill in)

- Vehicles / rate Hz / simulator mode: 100,000 / 1 Hz / kafka-direct, speedup 0, noise realistic, 180 s sim
- Kafka brokers / partitions / replication: 1 broker (KRaft, Docker Desktop) / 6 per oem.raw.* topic / RF 1
- Service replicas and CPU/memory limits: simulator 1 process, 12 workers, no limits; no gateway/consumers
- Dataset size: master data seed 42 (100K vehicles); code at 652609a (dirty only from evidence folders)
