# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing email, device, payment, IP, or time values mean unknown and contribute no evidence.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

Each field is reduced to a mutually exclusive agreement bucket. A pair receives `sum(log(m/u))`; centralized `m` arrays are validated positive distributions and `u` rates come from up to 100,000 deterministic pairs. Exact email, device, payment, and IP values use empirical value rarity. Subnets use level-level `u`, preventing a rare broad `/16` from becoming exact-like evidence. IP `m` is `0.45/0.20/0.05/0.30`, time `m` is monotonic `0.40/0.30/0.20/0.10`, and smoothing `0.5` prevents zero probabilities. Missing fields are omitted.

The selected raw threshold and fixed strong-pair threshold are both `3.0`; output confidence uses a numerically stable logistic transform. All parameters live in one config.

Batch caches all pair evidence. The selected average-linkage rule requires average raw evidence at threshold, one cross pair at the fixed strong threshold, and no hard constraint. Complete linkage remains implemented and tested.

## Engineering design

Indexes feed streaming only. Streaming skips conflicting clusters, tries later candidates, and applies average raw evidence plus the same strong guard. Its FS model remains fixed from initialization.

At 1M accounts, in-memory indexes and pair storage are the first pressure points. At 10M, DSU state, candidate postings, and complete cross-component checks need partitioning or durable storage. Cluster confidence is also quadratic in cluster size when batch output is written.

## Operating point

A predeclared coarse sweep selected average-strong at `3.0`: TP/FP/FN `23/1/9`, precision/recall/F1/F2 `95.83%/71.88%/82.14%/75.66%`. It recovered `1/2` fraud rings, affected two legitimate actors, and produced BusinessCost `2100`. Complete linkage at the same threshold had lower recall and F2. Streaming candidate recall was `31/32`. If false positives became more expensive, I would raise both evidence thresholds after held-out validation.

## Next steps

With another day I would estimate `m` from held-out labeled data or cautiously test EM, benchmark pair sampling at millions of records, and improve Unicode email normalization. The main limitation is that semi-empirical `m` values are not calibrated and fixed streaming parameters may become stale under distribution drift.
