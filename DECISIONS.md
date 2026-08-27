# Decisions

## Assumptions

- Account IDs are unique and `created_at` is valid RFC3339. Invalid records stop processing instead of producing partial, ambiguous state.
- Email local-part text after the first `+` is treated as an alias tag because this is common across major providers.
- Missing values mean unknown and add no evidence. Scores are renormalized over fields present in both records; timestamp alone is insufficient identity evidence.
- Constraints may mention accounts that arrive later in streaming mode, so all pairs are retained by account ID.
- Singleton confidence is `1.0`. Confidence is a deterministic compatibility heuristic, not a probability.

## Algorithm choice

The score uses email `0.40`, device `0.25`, payment fingerprint `0.25`, IP `0.08`, and creation time `0.02`, with threshold `0.45`. Exact normalized email scores `1`; non-exact email uses local-part Levenshtein, a `0.05` exact-domain bonus, and a `0.85` cap. Domains are never fuzzy matched. IP scores exact `1`, same `/24` (`/64`) `0.45`, and same `/16` (`/48`) `0.15`. All tunables live in `internal/similarity/config.go`.

Batch scores and caches every unordered pair, then uses deterministic complete linkage and hard constraints. An optional average-linkage rule requires average cross-pair score at threshold plus one pair at fixed `0.80`; it was evaluated but is not the default.

## Engineering design

Exact email, device, payment, IP, subnet, and email-trigram indexes feed only streaming candidate generation. Large blocks are capped at 256 entries and each event at 4,096 candidates. Streaming checks every cluster member, skips blocked clusters, and tries the next valid candidate.

At 1M accounts, in-memory indexes and pair storage are the first pressure points. At 10M, DSU state, candidate postings, and complete cross-component checks need partitioning or durable storage. Cluster confidence is also quadratic in cluster size when batch output is written.

## Operating point

A predeclared coarse threshold sweep selected complete linkage at `0.45`: TP/FP/FN `15/7/17`, precision/recall/F1/F2 `68.18%/46.88%/55.56%/50.00%`. It recovered `0/2` fraud rings, so sample BusinessCost is degenerate and was not used to select an extreme low-recall threshold. Streaming blocking covered `31/32` true sample pairs. If false positives became more expensive, I would raise the threshold.

## Next steps

With another day I would tune on held-out synthetic corruptions, add frequency-aware signal weights, benchmark million-record workloads, cache pair scores, improve Unicode/confusable email normalization, and replace hard candidate caps with deterministic rarest-block selection.
