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
	numerator := 0.0
	denominator := 0.0
	meaningful := false
	add := func(weight, value float64, available, identitySignal bool) {
		if !available || weight <= 0 {
			return
		}
		numerator += weight * value
		denominator += weight
		meaningful = meaningful || identitySignal
	}

	email, emailAvailable := s.emailSimilarity(a.Email, b.Email)
	device, deviceAvailable := exactSimilarity(a.DeviceID, b.DeviceID)
	payment, paymentAvailable := exactSimilarity(a.PaymentFingerprint, b.PaymentFingerprint)
	ip, ipAvailable := s.ipSimilarity(a.IP, b.IP)
	timeScore, timeAvailable := s.timeSimilarity(a.CreatedAt, b.CreatedAt)
	add(s.Config.EmailWeight, email, emailAvailable, true)
	add(s.Config.DeviceWeight, device, deviceAvailable, true)
	add(s.Config.PaymentWeight, payment, paymentAvailable, true)
	add(s.Config.IPWeight, ip, ipAvailable, true)
	add(s.Config.TimeWeight, timeScore, timeAvailable, false)
	if denominator == 0 || !meaningful {
		return 0
	}
	score := numerator / denominator
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func (s Scorer) emailSimilarity(a, b string) (float64, bool) {
	a = NormalizeEmail(a)
	b = NormalizeEmail(b)
	if a == "" || b == "" {
		return 0, false
	}
	return fuzzyEmailSimilarity(a, b, s.Config.EmailExactDomainBonus, s.Config.FuzzyEmailMax), true
}

func exactSimilarity(a, b string) (float64, bool) {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return 0, false
	}
	if a == b {
		return 1, true
	}
	return 0, true
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
	return fuzzyEmailSimilarity(a, b, config.EmailExactDomainBonus, config.FuzzyEmailMax)
}

func fuzzyEmailSimilarity(a, b string, exactDomainBonus, fuzzyMaximum float64) float64 {
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
		return minFloat(normalizedLevenshtein(a, b), fuzzyMaximum)
	}
	localWeight := 1 - exactDomainBonus
	result := localWeight * normalizedLevenshtein(aLocal, bLocal)
	if aDomain == bDomain {
		result += exactDomainBonus
	}
	return minFloat(result, fuzzyMaximum)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
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
	score, _ := s.ipSimilarity(a, b)
	return score
}

func (s Scorer) ipSimilarity(a, b string) (float64, bool) {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return 0, false
	}
	addrA, errA := netip.ParseAddr(a)
	addrB, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return 0, true
	}
	addrA = addrA.Unmap()
	addrB = addrB.Unmap()
	if addrA.Is4() != addrB.Is4() {
		return 0, true
	}
	if addrA == addrB {
		return 1, true
	}
	highBits := s.Config.IPv6HighPrefixBits
	midBits := s.Config.IPv6MidPrefixBits
	if addrA.Is4() {
		highBits = s.Config.IPv4HighPrefixBits
		midBits = s.Config.IPv4MidPrefixBits
	}
	if netip.PrefixFrom(addrA, highBits).Masked() == netip.PrefixFrom(addrB, highBits).Masked() {
		return s.Config.IPHighScore, true
	}
	if netip.PrefixFrom(addrA, midBits).Masked() == netip.PrefixFrom(addrB, midBits).Masked() {
		return s.Config.IPMidScore, true
	}
	return 0, true
}

func (s Scorer) TimeSimilarity(a, b time.Time) float64 {
	score, _ := s.timeSimilarity(a, b)
	return score
}

func (s Scorer) timeSimilarity(a, b time.Time) (float64, bool) {
	if a.IsZero() || b.IsZero() {
		return 0, false
	}
	delta := a.Sub(b)
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta <= s.Config.TimeVeryClose:
		return 1, true
	case delta <= s.Config.TimeClose:
		return s.Config.TimeCloseScore, true
	case delta <= s.Config.TimeModerate:
		return s.Config.TimeModerateScore, true
	case delta <= s.Config.TimeFar:
		return s.Config.TimeFarScore, true
	default:
		return 0, true
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
