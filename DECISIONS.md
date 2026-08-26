# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing email, device, payment, or IP values mean unknown. They receive a fixed score of `0.20`; weights are not renormalized.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

The baseline weighted pair scorer is unchanged. Its `[0,1]` compatibility becomes a signed graph edge with `score - 0.45`; positive edges favor co-clustering and negative edges favor separation. Batch precomputes every pair score, starts from singleton clusters, and repeatedly merges the legal cluster pair with the highest positive sum of cross-cluster edges. Maximizing internal signed weight is equivalent to the correlation objective up to a constant, and every accepted merge increases it by the reported gain.

Merge gains and cannot-link state are cached and updated additively. `verified_distinct` is a hard invalid merge rather than a large negative edge; a blocked best pair does not stop another legal merge. Node/account order provides deterministic tie-breaking. I skipped local search to keep this experiment focused and maintainable.

## Engineering design

Batch stores an `O(N^2)` edge matrix and uses a clear `O(N^3)` agglomerative scan, appropriate for the small experiment dataset but not millions of accounts. Streaming remains incremental: indexes identify candidate clusters, insertion gain is the sum of signed edges to all cluster members, constrained clusters are skipped, and unrelated historical accounts are never reclustered. This is an online approximation of the batch objective.

At 1M accounts, the pair matrix fails first; a production version would require sparse candidate edges, a priority queue, and partitioned clustering. Cluster confidence is the average internal baseline similarity and remains quadratic in cluster size when output is written.

## Operating point

The `main` baseline produced TP/FP/TN/FN `16/2/4916/16` and accuracy/precision/recall/F1 `99.6364%/88.8889%/50%/64%`. A neutral-similarity sweep from `0.30` to `0.60` selected `0.45`: `16/3/4915/16` and `99.6162%/84.2105%/50%/62.7451%`. The global heuristic does not beat the conservative baseline on this sample; it adds one false-positive pair without recovering a positive pair. If false positives became more expensive, I would increase the neutral point.

## Next steps

With another day I would add deterministic single-node local refinement, test sparse-edge variants on larger held-out data, and benchmark a priority-queue gain cache. The key limitation is that dense agglomeration is cubic and greedy merges cannot be undone.
