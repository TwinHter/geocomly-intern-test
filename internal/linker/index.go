package linker

import (
	"net/netip"
	"sort"
	"strings"

	"accountlinker/internal/model"
	"accountlinker/internal/similarity"
)

type candidateIndex struct {
	config similarity.Config

	emails       map[string][]int
	emailNGrams  map[string][]int
	devices      map[string][]int
	payments     map[string][]int
	ipExact      map[string][]int
	ipHighSubnet map[string][]int
	ipMidSubnet  map[string][]int
}

func newCandidateIndex(config similarity.Config) *candidateIndex {
	return &candidateIndex{
		config:       config,
		emails:       make(map[string][]int),
		emailNGrams:  make(map[string][]int),
		devices:      make(map[string][]int),
		payments:     make(map[string][]int),
		ipExact:      make(map[string][]int),
		ipHighSubnet: make(map[string][]int),
		ipMidSubnet:  make(map[string][]int),
	}
}

func (idx *candidateIndex) add(account model.Account, accountIndex int) {
	appendKey(idx.emails, similarity.NormalizeEmail(account.Email), accountIndex)
	for _, ngram := range similarity.EmailNGrams(account.Email, idx.config.EmailNGramSize) {
		appendKey(idx.emailNGrams, ngram, accountIndex)
	}
	appendKey(idx.devices, strings.TrimSpace(account.DeviceID), accountIndex)
	appendKey(idx.payments, strings.TrimSpace(account.PaymentFingerprint), accountIndex)
	exact, high, mid := idx.ipKeys(account.IP)
	appendKey(idx.ipExact, exact, accountIndex)
	appendKey(idx.ipHighSubnet, high, accountIndex)
	appendKey(idx.ipMidSubnet, mid, accountIndex)
}

func appendKey(index map[string][]int, key string, accountIndex int) {
	if key != "" {
		index[key] = append(index[key], accountIndex)
	}
}

func (idx *candidateIndex) candidates(account model.Account) []int {
	strong := make(map[int]struct{})
	idx.collect(strong, idx.emails[similarity.NormalizeEmail(account.Email)])
	idx.collect(strong, idx.devices[strings.TrimSpace(account.DeviceID)])
	idx.collect(strong, idx.payments[strings.TrimSpace(account.PaymentFingerprint)])
	exact, high, mid := idx.ipKeys(account.IP)
	idx.collect(strong, idx.ipExact[exact])
	idx.collect(strong, idx.ipHighSubnet[high])
	idx.collect(strong, idx.ipMidSubnet[mid])

	ngramMatches := make(map[int]int)
	for _, ngram := range similarity.EmailNGrams(account.Email, idx.config.EmailNGramSize) {
		posting := idx.emailNGrams[ngram]
		if len(posting) > idx.config.MaxBlockSize {
			continue
		}
		for _, candidate := range posting {
			ngramMatches[candidate]++
		}
	}
	for candidate, matches := range ngramMatches {
		if matches >= idx.config.MinEmailNGramMatches ||
			similarity.RuneLength(similarity.EmailLocal(account.Email)) < idx.config.ShortEmailRuneLength {
			strong[candidate] = struct{}{}
		}
	}

	result := make([]int, 0, len(strong))
	for candidate := range strong {
		result = append(result, candidate)
	}
	sort.Ints(result)
	if len(result) > idx.config.MaxCandidates {
		result = result[:idx.config.MaxCandidates]
	}
	return result
}

func (idx *candidateIndex) collect(destination map[int]struct{}, posting []int) {
	if len(posting) == 0 || len(posting) > idx.config.MaxBlockSize {
		return
	}
	for _, candidate := range posting {
		destination[candidate] = struct{}{}
	}
}

func (idx *candidateIndex) ipKeys(raw string) (exact, high, mid string) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", "", ""
	}
	addr = addr.Unmap()
	highBits := idx.config.IPv6HighPrefixBits
	midBits := idx.config.IPv6MidPrefixBits
	if addr.Is4() {
		highBits = idx.config.IPv4HighPrefixBits
		midBits = idx.config.IPv4MidPrefixBits
	}
	return addr.String(), netip.PrefixFrom(addr, highBits).Masked().String(), netip.PrefixFrom(addr, midBits).Masked().String()
}

// StreamingCandidatePairs exposes deterministic blocking coverage for offline diagnostics.
func StreamingCandidatePairs(accounts []model.Account, config similarity.Config) [][2]string {
	ordered := append([]model.Account(nil), accounts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].AccountID < ordered[j].AccountID })
	idx := newCandidateIndex(config)
	pairs := make([][2]string, 0)
	for i, account := range ordered {
		for _, candidate := range idx.candidates(account) {
			pairs = append(pairs, [2]string{ordered[candidate].AccountID, account.AccountID})
		}
		idx.add(account, i)
	}
	return pairs
}
