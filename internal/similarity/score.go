package similarity

import (
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"accountlinker/internal/model"
)

type frequencies struct {
	total    int
	emails   map[string]int
	devices  map[string]int
	payments map[string]int
	ipExact  map[string]int
	ipHigh   map[string]int
	ipMid    map[string]int
}

// Evidence exposes the normalized score and its raw field contributions.
type Evidence struct {
	Raw     float64
	Score   float64
	Email   float64
	Device  float64
	Payment float64
	IP      float64
	Time    float64
}

type Scorer struct {
	Config Config
	stats  frequencies
}

func New(config Config, accounts []model.Account) *Scorer {
	scorer := &Scorer{
		Config: config,
		stats: frequencies{
			emails:   make(map[string]int),
			devices:  make(map[string]int),
			payments: make(map[string]int),
			ipExact:  make(map[string]int),
			ipHigh:   make(map[string]int),
			ipMid:    make(map[string]int),
		},
	}
	for _, account := range accounts {
		scorer.Add(account)
	}
	return scorer
}

// Add updates frequency state after a streamed account has been assigned.
func (s *Scorer) Add(account model.Account) {
	s.stats.total++
	increment(s.stats.emails, NormalizeEmail(account.Email))
	increment(s.stats.devices, strings.TrimSpace(account.DeviceID))
	increment(s.stats.payments, strings.TrimSpace(account.PaymentFingerprint))
	exact, high, mid := IPKeys(account.IP, s.Config)
	increment(s.stats.ipExact, exact)
	increment(s.stats.ipHigh, high)
	increment(s.stats.ipMid, mid)
}

func increment(counts map[string]int, value string) {
	if value != "" {
		counts[value]++
	}
}

func (s *Scorer) Score(a, b model.Account) float64 {
	return s.PairEvidence(a, b).Score
}

func (s *Scorer) PairEvidence(a, b model.Account) Evidence {
	result := Evidence{
		Email:   s.emailEvidence(a.Email, b.Email),
		Device:  s.exactEvidence(a.DeviceID, b.DeviceID, s.stats.devices),
		Payment: s.exactEvidence(a.PaymentFingerprint, b.PaymentFingerprint, s.stats.payments),
		IP:      s.ipEvidence(a.IP, b.IP),
		Time:    s.timeEvidence(a.CreatedAt, b.CreatedAt),
	}
	result.Raw = result.Email + result.Device + result.Payment + result.IP + result.Time
	maxRarity := s.maximumRarity()
	if s.Config.EvidenceScale > 0 && maxRarity > 0 {
		result.Score = 1 - math.Exp(-result.Raw/(s.Config.EvidenceScale*maxRarity))
	}
	return result
}

func (s *Scorer) maximumRarity() float64 {
	if s.stats.total == 0 {
		return 0
	}
	smoothing := s.Config.Smoothing
	if smoothing <= 0 {
		smoothing = 1
	}
	return math.Log((float64(s.stats.total) + 2*smoothing) / smoothing)
}

func (s *Scorer) rarity(counts map[string]int, value string) float64 {
	if value == "" || s.stats.total == 0 {
		return 0
	}
	smoothing := s.Config.Smoothing
	if smoothing <= 0 {
		smoothing = 1
	}
	probability := (float64(counts[value]) + smoothing) /
		(float64(s.stats.total) + 2*smoothing)
	if probability >= 1 {
		return 0
	}
	return -math.Log(probability)
}

func (s *Scorer) emailEvidence(a, b string) float64 {
	a = NormalizeEmail(a)
	b = NormalizeEmail(b)
	if a == "" || b == "" {
		return 0
	}
	rarity := math.Min(s.rarity(s.stats.emails, a), s.rarity(s.stats.emails, b))
	if a == b {
		return rarity
	}
	return normalizedLevenshtein(EmailLocal(a), EmailLocal(b)) * rarity
}

func (s *Scorer) exactEvidence(a, b string, counts map[string]int) float64 {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || a != b {
		return 0
	}
	return s.rarity(counts, a)
}

func (s *Scorer) ipEvidence(a, b string) float64 {
	aExact, aHigh, aMid := IPKeys(a, s.Config)
	bExact, bHigh, bMid := IPKeys(b, s.Config)
	if aExact == "" || bExact == "" {
		return 0
	}
	if aExact == bExact {
		return s.rarity(s.stats.ipExact, aExact)
	}
	if aHigh == bHigh {
		return s.Config.IPHighFactor * s.rarity(s.stats.ipHigh, aHigh)
	}
	if aMid == bMid {
		return s.Config.IPMidFactor * s.rarity(s.stats.ipMid, aMid)
	}
	return 0
}

func (s *Scorer) timeEvidence(a, b time.Time) float64 {
	if a.IsZero() || b.IsZero() || s.Config.TimeDecay <= 0 {
		return 0
	}
	delta := a.Sub(b)
	if delta < 0 {
		delta = -delta
	}
	return s.Config.TimeEvidence / (1 + float64(delta)/float64(s.Config.TimeDecay))
}

func NormalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	local, domain, found := strings.Cut(email, "@")
	if !found {
		return email
	}
	if base, _, tagged := strings.Cut(local, "+"); tagged {
		local = base
	}
	return local + "@" + domain
}

func EmailLocal(email string) string {
	email = NormalizeEmail(email)
	local, _, found := strings.Cut(email, "@")
	if !found {
		return email
	}
	return local
}

func EmailSimilarity(a, b string) float64 {
	a = NormalizeEmail(a)
	b = NormalizeEmail(b)
	if a == b {
		return 1
	}
	return normalizedLevenshtein(EmailLocal(a), EmailLocal(b))
}

func normalizedLevenshtein(a, b string) float64 {
	if a == b {
		return 1
	}
	ar := []rune(a)
	br := []rune(b)
	maxLen := len(ar)
	if len(br) > maxLen {
		maxLen = len(br)
	}
	if maxLen == 0 {
		return 1
	}
	distance := levenshtein(ar, br)
	return 1 - float64(distance)/float64(maxLen)
}

func levenshtein(a, b []rune) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	previous := make([]int, len(a)+1)
	current := make([]int, len(a)+1)
	for i := range previous {
		previous[i] = i
	}
	for j, rb := range b {
		current[0] = j + 1
		for i, ra := range a {
			cost := 0
			if ra != rb {
				cost = 1
			}
			current[i+1] = min3(current[i]+1, previous[i+1]+1, previous[i]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(a)]
}

func min3(a, b, c int) int {
	if a < b && a < c {
		return a
	}
	if b < c {
		return b
	}
	return c
}

func IPKeys(raw string, config Config) (exact, high, mid string) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", "", ""
	}
	addr = addr.Unmap()
	highBits := config.IPv6HighPrefixBits
	midBits := config.IPv6MidPrefixBits
	if addr.Is4() {
		highBits = config.IPv4HighPrefixBits
		midBits = config.IPv4MidPrefixBits
	}
	return addr.String(), netip.PrefixFrom(addr, highBits).Masked().String(), netip.PrefixFrom(addr, midBits).Masked().String()
}

func EmailNGrams(email string, size int) []string {
	local := EmailLocal(email)
	if local == "" || size <= 0 {
		return nil
	}
	runes := []rune(local)
	if len(runes) < size {
		return []string{local}
	}
	seen := make(map[string]struct{}, len(runes)-size+1)
	for i := 0; i+size <= len(runes); i++ {
		seen[string(runes[i:i+size])] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for ngram := range seen {
		result = append(result, ngram)
	}
	sort.Strings(result)
	return result
}

func RuneLength(value string) int {
	return utf8.RuneCountInString(value)
}
