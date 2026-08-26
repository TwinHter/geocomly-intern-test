# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing email, device, payment, or IP values mean unknown. They receive a fixed score of `0.20`; weights are not renormalized.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

The score is a fixed weighted sum: email `0.50`, device `0.175`, payment fingerprint `0.175`, IP `0.10`, and creation time `0.05`, with a merge threshold of `0.44`. Email is lowercased, trimmed, stripped of a `+tag`, then compared with rune-aware Levenshtein similarity split `85%` local part and `15%` domain. IP scores exact, same IPv4 `/24` or IPv6 `/64`, and same IPv4 `/16` or IPv6 `/48`; time uses configurable distance buckets. All tunable values live in `internal/similarity/config.go`.

Batch mode generates blocked candidate pairs, sorts passing pairs by score and account ID, and uses DSU only for component bookkeeping. A union occurs only if every cross-component pair meets the threshold and no pair is `verified_distinct`. Rejected merges do not stop later candidates. This conservative complete-linkage rule limits false-positive blast radius while still favoring the assignment's higher false-negative cost through fuzzy email and subnet candidates.

## Engineering design

Exact email, device, payment, IP, subnet, and email-trigram indexes feed both batch and streaming paths; indexes never decide membership. Large blocks are capped at 256 entries and each event at 4,096 candidate accounts to protect latency from shared hubs. Streaming evaluates candidate clusters incrementally, skips any conflicting cluster, chooses the highest minimum member score, preserves existing IDs, and creates sequential IDs for singletons.

At 1M accounts, in-memory indexes and pair storage are the first pressure points. At 10M, DSU state, candidate postings, and complete cross-component checks need partitioning or durable storage. Cluster confidence is also quadratic in cluster size when batch output is written.

## Operating point

On the provided sample, a 408-configuration pairwise sweep improved the baseline from accuracy/precision/recall/F1 of `99.5354%/100%/28.125%/43.9024%` to `99.6364%/88.8889%/50%/64%`. The selected `0.44` threshold and email-heavy weights favor recall because missed fraud rings are much more expensive, while complete linkage and verified-distinct checks limit false-positive propagation. These sample metrics are diagnostic rather than evidence of generalization. If false positives became much more expensive, I would raise the threshold, lower IP/time weights, and require two non-missing strong signals.

## Next steps

With another day I would tune on held-out synthetic corruptions, add frequency-aware signal weights, benchmark million-record workloads, cache pair scores, improve Unicode/confusable email normalization, and replace hard candidate caps with deterministic rarest-block selection.
