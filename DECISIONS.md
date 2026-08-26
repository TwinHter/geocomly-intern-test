# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing email, device, payment, or IP values mean unknown and contribute no evidence.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

The experimental score is frequency-aware log evidence. For `N` current accounts, a value with frequency `f` contributes `-log((f+s)/(N+2s))`: rare matching normalized emails, devices, payments, and IP representations therefore carry more identity evidence than shared hubs. Fuzzy email evidence is local-part Levenshtein similarity multiplied by the more conservative rarity of the two normalized emails. IP uses one non-overlapping level: exact, `/24` (`/64` for IPv6), or `/16` (`/48`), with broader levels discounted. Time adds one small smoothly decaying term. Missing signals add zero.

Raw contributions are summed and mapped monotonically to `[0,1]` with `1-exp(-raw/(scale*maximumRarity))`; corpus-relative normalization keeps the `0.60` merge threshold more stable across dataset sizes. The major scoring parameters are smoothing `1`, evidence scale `0.75`, threshold `0.60`, subnet factors `0.60/0.25`, and time evidence `0.25` with a seven-day decay. All tunables live in `internal/similarity/config.go`; there are no independent per-field weights.

Batch mode generates blocked candidate pairs, sorts passing pairs by score and account ID, and uses DSU only for component bookkeeping. A union occurs only if every cross-component pair meets the threshold and no pair is `verified_distinct`. Rejected merges do not stop later candidates. This conservative complete-linkage rule limits false-positive blast radius while still favoring the assignment's higher false-negative cost through fuzzy email and subnet candidates.

## Engineering design

Exact email, device, payment, IP, subnet, and email-trigram indexes feed both batch and streaming paths; indexes never decide membership. Large blocks are capped at 256 entries and each event at 4,096 candidate accounts to protect latency from shared hubs. Streaming evaluates candidate clusters incrementally, skips any conflicting cluster, chooses the highest minimum member score, then updates frequency tables and indexes after assignment.

At 1M accounts, in-memory indexes and pair storage are the first pressure points. At 10M, DSU state, candidate postings, and complete cross-component checks need partitioning or durable storage. Cluster confidence is also quadratic in cluster size when batch output is written.

## Operating point

The `main` baseline produced TP/FP/TN/FN `16/2/4916/16` and pairwise accuracy/precision/recall/F1 `99.6364%/88.8889%/50%/64%`. A threshold sweep from `0.45` to `0.82` selected `0.60` for this experiment: `17/6/4912/15` and `99.5758%/73.9130%/53.1250%/61.8182%`. It trades four additional false-positive pairs for one recovered positive pair; this is consistent with the stated FN cost, but it does not beat the tuned baseline F1 and needs held-out validation. If false positives became more expensive, I would raise the threshold.

## Next steps

With another day I would validate on held-out corruptions, benchmark million-record frequency maps, cache pair evidence, improve Unicode/confusable email normalization, and replace hard candidate caps with deterministic rarest-block selection. A known limitation is that corpus frequencies drift during streaming, so scores for old pairs change over time while earlier cluster assignments are intentionally not revisited.
