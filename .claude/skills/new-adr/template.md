# ADR-NNNN: <Decision title>

- **Status:** Proposed <!-- Proposed | Accepted | Superseded by ADR-XXXX | Deprecated -->
- **Date:** YYYY-MM-DD
- **Deciders:** <names/roles>
- **Related:** CLAUDE.md §<x>, ADR-<y>, feature F-<zz>

## Context

<What forces are at play? What problem, at what scale? Quote the relevant NFR targets (e.g. 100K events/s, API p95 < 200 ms). 3–8 sentences.>

## Decision drivers

1. <e.g. Sustain 100K events/s with a 3× burst for 5 min, no data loss>
2. <e.g. Per-VIN ordering>
3. <e.g. Cloud-agnostic (runs on AWS and Azure with no code change)>
4. <e.g. Team skill / time to build within the hackathon>

## Options considered

| Option | Summary | Pros | Cons |
|--------|---------|------|------|
| A. <chosen> | | | |
| B. <strongest alternative> | | | |
| C. <optional> | | | |

<Optional: score each option 1–5 against each driver.>

## Decision

We will use **<option>** because <2–4 sentences tied to the drivers>.

### CAP / PACELC

<For data or messaging decisions: CP or AP under partition? Latency or consistency else (PACELC)? Per data class if it differs. Otherwise "N/A".>

## Consequences

**Positive**
- …

**Negative / risks → mitigation**
- … → …

**Follow-ups**
- [ ] <task, e.g. add Testcontainers test for …>

## Evidence

- <link to docs/evidence/... or "TBD – measure with /evidence">

## Revisit trigger

<Measurable condition that would make us reopen this decision.>

## Implementation

- <code paths, e.g. services/ingest-gateway/internal/adapters/kafka/>
