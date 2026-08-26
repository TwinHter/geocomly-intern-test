package similarity

import (
	"net/netip"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"accountlinker/internal/model"
)

type Scorer struct {
	Config Config
}

func New(config Config) Scorer {
	return Scorer{Config: config}
}

func (s Scorer) Score(a, b model.Account) float64 {
	score := s.Config.EmailWeight*s.emailSimilarity(a.Email, b.Email) +
		s.Config.DeviceWeight*s.exactOrMissing(a.DeviceID, b.DeviceID) +
		s.Config.PaymentWeight*s.exactOrMissing(a.PaymentFingerprint, b.PaymentFingerprint) +
		s.Config.IPWeight*s.IPSimilarity(a.IP, b.IP) +
		s.Config.TimeWeight*s.TimeSimilarity(a.CreatedAt, b.CreatedAt)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func (s Scorer) emailSimilarity(a, b string) float64 {
	if NormalizeEmail(a) == "" || NormalizeEmail(b) == "" {
		return s.Config.MissingValueScore
	}
	return weightedEmailSimilarity(
		a,
		b,
		s.Config.EmailLocalPartWeight,
		s.Config.EmailDomainPartWeight,
	)
}

func (s Scorer) exactOrMissing(a, b string) float64 {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return s.Config.MissingValueScore
	}
	if a == b {
		return 1
	}
	return 0
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
	config := DefaultConfig()
	return weightedEmailSimilarity(a, b, config.EmailLocalPartWeight, config.EmailDomainPartWeight)
}

func weightedEmailSimilarity(a, b string, localWeight, domainWeight float64) float64 {
	a = NormalizeEmail(a)
	b = NormalizeEmail(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	aLocal, aDomain, aFound := strings.Cut(a, "@")
	bLocal, bDomain, bFound := strings.Cut(b, "@")
	if !aFound || !bFound {
		return normalizedLevenshtein(a, b)
	}
	return localWeight*normalizedLevenshtein(aLocal, bLocal) +
		domainWeight*normalizedLevenshtein(aDomain, bDomain)
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

func (s Scorer) IPSimilarity(a, b string) float64 {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return s.Config.MissingValueScore
	}
	addrA, errA := netip.ParseAddr(a)
	addrB, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return 0
	}
	addrA = addrA.Unmap()
	addrB = addrB.Unmap()
	if addrA.Is4() != addrB.Is4() {
		return 0
	}
	if addrA == addrB {
		return 1
	}
	highBits := s.Config.IPv6HighPrefixBits
	midBits := s.Config.IPv6MidPrefixBits
	if addrA.Is4() {
		highBits = s.Config.IPv4HighPrefixBits
		midBits = s.Config.IPv4MidPrefixBits
	}
	if netip.PrefixFrom(addrA, highBits).Masked() == netip.PrefixFrom(addrB, highBits).Masked() {
		return s.Config.IPHighScore
	}
	if netip.PrefixFrom(addrA, midBits).Masked() == netip.PrefixFrom(addrB, midBits).Masked() {
		return s.Config.IPMidScore
	}
	return 0
}

func (s Scorer) TimeSimilarity(a, b time.Time) float64 {
	if a.IsZero() || b.IsZero() {
		return s.Config.MissingValueScore
	}
	delta := a.Sub(b)
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta <= s.Config.TimeVeryClose:
		return 1
	case delta <= s.Config.TimeClose:
		return s.Config.TimeCloseScore
	case delta <= s.Config.TimeModerate:
		return s.Config.TimeModerateScore
	case delta <= s.Config.TimeFar:
		return s.Config.TimeFarScore
	default:
		return 0
	}
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
	for trigram := range seen {
		result = append(result, trigram)
	}
	sort.Strings(result)
	return result
}

func RuneLength(value string) int {
	return utf8.RuneCountInString(value)
}
