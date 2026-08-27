# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing email, device, payment, or IP values mean unknown and contribute no evidence.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

The experimental score is frequency-aware log evidence. For `N` current accounts, a value with frequency `f` contributes `-log((f+s)/(N+2s))`: rare matching normalized emails, devices, payments, and IP representations carry more evidence than shared hubs. Fuzzy email uses only local-part Levenshtein times conservative email rarity. IP uses exact, `/24` (`/64`) at factor `0.45`, or weak `/16` (`/48`) at `0.10`. Time adds one small decaying term. Missing signals add zero.

Raw contributions are mapped to `[0,1]` with `1-exp(-raw/(scale*maximumRarity))`. Parameters are smoothing `1`, scale `0.75`, threshold `0.60`, and time evidence `0.25` with seven-day decay. All tunables live in one config.

Batch scores and caches all unordered pairs. Default complete linkage checks every cross pair. The evaluated alternative requires average score at threshold, one pair at fixed `0.75`, and no hard constraint.

## Engineering design

Exact email, device, payment, IP, subnet, and email-trigram indexes feed streaming only. Streaming skips invalid clusters, chooses the best valid cluster under the configured linkage rule, then updates frequencies and indexes after assignment.

At 1M accounts, in-memory indexes and pair storage are the first pressure points. At 10M, DSU state, candidate postings, and complete cross-component checks need partitioning or durable storage. Cluster confidence is also quadratic in cluster size when batch output is written.

## Operating point

A predeclared coarse sweep selected complete linkage at `0.60`: TP/FP/FN `17/6/15`, precision/recall/F1/F2 `73.91%/53.13%/61.82%/56.29%`. It recovered `0/2` fraud rings and affected seven legitimate actors. Since every setting missed both rings, selection used F2/F1 rather than the degenerate BusinessCost. Streaming candidate recall was `31/32`. If false positives became more expensive, I would raise the threshold.

## Next steps

With another day I would validate on held-out corruptions, benchmark million-record frequency maps, cache pair evidence, improve Unicode/confusable email normalization, and replace hard candidate caps with deterministic rarest-block selection. A known limitation is that corpus frequencies drift during streaming, so scores for old pairs change over time while earlier cluster assignments are intentionally not revisited.
