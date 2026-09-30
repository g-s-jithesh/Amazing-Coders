-- One cluster, one schema per owning service (CLAUDE.md §5.2).
CREATE EXTENSION IF NOT EXISTS vector;
CREATE SCHEMA IF NOT EXISTS fleet;
CREATE SCHEMA IF NOT EXISTS battery;
CREATE SCHEMA IF NOT EXISTS dispatch;
