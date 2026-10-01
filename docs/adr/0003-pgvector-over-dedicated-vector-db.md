# ADR-0003: pgvector over a dedicated vector database

- **Status:** Accepted
- **Date:** 2026-10-01
- **Deciders:** Team Amazing Coders (drafted with Claude)
- **Related:** CLAUDE.md §5.1, §8.2

## Context
Runbook chunks (about 10^3-10^4) and fault signatures (about 10^5-10^6) must be searched by similarity, joined with
tenant and vehicle data, and filtered by tenant.

## Options considered
1. **Qdrant / Weaviate**: purpose-built, but one more system, one more auth and backup path, and tenant joins happen
   in application code.
2. **pgvector in the existing Postgres (chosen)**: HNSW cosine, SQL joins and RLS apply directly, nothing new to run.

## Decision
pgvector. **Revisit trigger:** filtered kNN recall below 0.95 vs an exact scan, or p95 above 100 ms, at the real corpus size.

## Consequences
- Similarity search (F-17) and runbook RAG are **not built** in this submission; the image already ships the
  `pgvector/pgvector:pg16` extension so the decision costs nothing until they are.
