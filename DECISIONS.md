# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing email, device, payment, IP, or time values mean unknown and contribute no evidence.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

Each field is reduced to a small agreement bucket. Email uses exact/very-high/medium/low local-part similarity; device and payment use exact/different; IP uses exact, `/24` (`/64` IPv6), `/16` (`/48`), or different; time uses four broad proximity buckets. A pair receives `sum(log(m/u))`, where centralized `m` arrays are sensible same-actor initial assumptions and `u` bucket rates come from up to 100,000 deterministic unordered pairs in the initial dataset. Exact values use their own empirical frequency for `u`, so rare device/payment matches provide more evidence than common shared values. Smoothing `0.5` prevents zero probabilities. These estimates are not claimed to be calibrated.

The raw link threshold is `1.0`; output confidence is the logistic transform of raw evidence. All parameters live in `internal/similarity/config.go`. There are no manually assigned per-field weights.

Batch mode generates blocked candidate pairs, sorts passing pairs by score and account ID, and uses DSU only for component bookkeeping. A union occurs only if every cross-component pair meets the threshold and no pair is `verified_distinct`. Rejected merges do not stop later candidates. This conservative complete-linkage rule limits false-positive blast radius while still favoring the assignment's higher false-negative cost through fuzzy email and subnet candidates.

## Engineering design

Exact email, device, payment, IP, subnet, and email-trigram indexes feed both batch and streaming paths; indexes never decide membership. Large blocks are capped at 256 entries and each event at 4,096 candidate accounts. Streaming skips conflicting clusters and tries later candidates as before. Its Fellegi-Sunter model is fixed from the initial dataset to avoid changing compatibility for existing clusters; only indexes and cluster state update online.

At 1M accounts, in-memory indexes and pair storage are the first pressure points. At 10M, DSU state, candidate postings, and complete cross-component checks need partitioning or durable storage. Cluster confidence is also quadratic in cluster size when batch output is written.

## Operating point

The `main` baseline produced TP/FP/TN/FN `16/2/4916/16` and accuracy/precision/recall/F1 `99.6364%/88.8889%/50%/64%`. A raw-threshold sweep from `-1` to `4` selected `1.0` for this experiment: `20/10/4908/12` and `99.5556%/66.6667%/62.5%/64.5161%`. The added recall fits the much higher stated FN cost, though the precision reduction requires held-out validation. If false positives became more expensive, I would raise the evidence threshold.

## Next steps

With another day I would estimate `m` from held-out labeled data or cautiously test EM, benchmark pair sampling at millions of records, and improve Unicode email normalization. The main limitation is that semi-empirical `m` values are not calibrated and fixed streaming parameters may become stale under distribution drift.
