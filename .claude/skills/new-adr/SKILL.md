---
name: new-adr
description: Create a new Architecture Decision Record in docs/adr/ (Context, Options, Decision, Consequences, CAP/PACELC). Use when adding a datastore, broker, language or cross-service contract, when choosing between design options, or when the user asks for an ADR.
argument-hint: "<short decision title, e.g. 'pgvector over Qdrant for fault signatures'>"
---

# New ADR

Create an Architecture Decision Record for: **$ARGUMENTS**

The Solution Document (§12) needs 3–5 ADRs that cover databases, messaging and at least one CAP trade-off. ADRs also back up the "why this, and what you rejected" column in §5.2. Write them as evidence, not as essays.

## Steps

1. **Find the next number.** List `docs/adr/`, take the highest `NNNN-*.md`, and add 1 (4 digits, zero-padded). The slug is the title in kebab-case, at most 6 words. File: `docs/adr/NNNN-<slug>.md`.
2. **Check for overlap.** Grep `docs/adr/` for the same topic. If an earlier ADR covers it, this one either **supersedes** it (set the old one's status to `Superseded by NNNN` and link both ways) or should be an amendment to it. Ask the user if it's unclear.
3. **Gather context from the repo, not from memory.**
   - the relevant CLAUDE.md sections (root and the service's own CLAUDE.md)
   - the code the decision affects (grep for the component)
   - any evidence in `docs/evidence/` (benchmarks, query plans)
4. **Write the ADR from [template.md](template.md).** Rules:
   - **At least 2 real options**, including the obvious alternative a judge would ask about (e.g. RabbitMQ vs Kafka, Qdrant vs pgvector, exactly-once vs at-least-once). Score each against the drivers.
   - **Decision drivers** must trace back to the NFRs in CLAUDE.md §2: throughput, latency, availability, security, cost, cloud-agnosticism, team skill, time to build.
   - **CAP/PACELC:** fill this in for any decision about data storage or messaging. Otherwise write "N/A".
   - **Consequences:** include negatives and the mitigation for each. An ADR with no downside reads as not thought through.
   - **Revisit trigger:** a measurable condition, e.g. "if kNN p95 > 50 ms at 1M vectors".
   - **Numbers:** only ones with a link to `docs/evidence/...`. Otherwise write `TBD – measure with /evidence`. Never estimate throughput or latency as if it were measured.
   - **Links:** the code paths that implement the decision.
5. **Status** starts as `Proposed`. Change it to `Accepted` only when the user confirms, or when the implementing code is merged.
6. **Update the index** in `docs/adr/README.md` (number, title, status, date). Create the index if it's missing.
7. **Cross-reference:**
   - if the decision changes architecture described in the root CLAUDE.md (§3, §5, §16), update CLAUDE.md in the same change
   - if it's a new datastore, broker or language, confirm the polyglot table (§5.1) and the tech-stack rationale stay consistent
8. **Report** the file path, the status, and anything left as `TBD`.

## Quality bar (self-check before finishing)

- [ ] Someone who wasn't in the discussion can understand why we chose this in under 2 minutes.
- [ ] The rejected option is described fairly (a steelman, not a strawman).
- [ ] Every number is either linked evidence or marked TBD.
- [ ] Downsides and mitigations are listed.
- [ ] It has a revisit trigger.
