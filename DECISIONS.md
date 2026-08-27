# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing values mean unknown and add no evidence. Scores are renormalized over fields present in both records; timestamp alone is insufficient.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

The corrected baseline uses email/device/payment/IP/time weights `0.40/0.25/0.25/0.08/0.02`, local-only fuzzy email with a small exact-domain bonus, and weak `/16` evidence. Compatibility becomes `score - 0.45`. Batch starts from singletons and selects the legal merge with highest positive average cross-cluster edge. Average support removes the old size bias while every accepted merge still increases the raw signed objective.

Raw edge sums and cannot-link state are cached and updated additively; sums are divided by cross-pair count only for selection. `verified_distinct` is a hard reject, and account order provides deterministic tie-breaking.

## Engineering design

Batch stores an `O(N^2)` edge matrix and uses a clear `O(N^3)` agglomerative scan. Streaming indexes candidate clusters, uses average signed-edge insertion support, skips constrained clusters, and never reclusters historical accounts.

At 1M accounts, the pair matrix fails first; a production version would require sparse candidate edges, a priority queue, and partitioned clustering. Cluster confidence is the average internal baseline similarity and remains quadratic in cluster size when output is written.

## Operating point

A predeclared neutral sweep selected `0.45` by F2: TP/FP/FN `15/11/17`, precision/recall/F1/F2 `57.69%/46.88%/51.72%/48.70%`. It recovered `0/2` rings and affected ten legitimate actors. BusinessCost is degenerate because every setting missed both rings. Raw-sum and average support happened to match on the sample, but a regression graph demonstrates the old large-cluster bias. If false positives became more expensive, I would increase the neutral point.

## Next steps

With another day I would add deterministic single-node local refinement, test sparse-edge variants on larger held-out data, and benchmark a priority-queue gain cache. The key limitation is that dense agglomeration is cubic and greedy merges cannot be undone.
