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

const (
	emailExact = iota
	emailVeryHigh
	emailMedium
	emailLow
	emailLevels
)

const (
	categoricalExact = iota
	categoricalDifferent
	categoricalLevels
)

const (
	ipExact = iota
	ipHigh
	ipMid
	ipDifferent
	ipLevels
)

const (
	timeVeryClose = iota
	timeClose
	timeModerate
	timeFar
	timeLevels
)

type empiricalModel struct {
	accountCount int
	pairCount    int
	emails       map[string]int
	devices      map[string]int
	payments     map[string]int
	ipExact      map[string]int
	ipHigh       map[string]int
	ipMid        map[string]int
	emailU       [emailLevels]float64
	deviceU      [categoricalLevels]float64
	paymentU     [categoricalLevels]float64
	ipU          [ipLevels]float64
	timeU        [timeLevels]float64
}

// Evidence retains raw log-likelihood evidence and output confidence.
type Evidence struct {
	Raw        float64
	Confidence float64
	Email      float64
	Device     float64
	Payment    float64
	IP         float64
	Time       float64
}

type Scorer struct {
	Config Config
	model  empiricalModel
}

func New(config Config, accounts []model.Account) *Scorer {
	scorer := &Scorer{Config: config}
	scorer.model = estimateModel(accounts, config)
	return scorer
}

func estimateModel(accounts []model.Account, config Config) empiricalModel {
	result := empiricalModel{
		accountCount: len(accounts),
		emails:       make(map[string]int),
		devices:      make(map[string]int),
		payments:     make(map[string]int),
		ipExact:      make(map[string]int),
		ipHigh:       make(map[string]int),
		ipMid:        make(map[string]int),
	}
	for _, account := range accounts {
		increment(result.emails, NormalizeEmail(account.Email))
		increment(result.devices, strings.TrimSpace(account.DeviceID))
		increment(result.payments, strings.TrimSpace(account.PaymentFingerprint))
		exact, high, mid := IPKeys(account.IP, config)
		increment(result.ipExact, exact)
		increment(result.ipHigh, high)
		increment(result.ipMid, mid)
	}

	var emailCounts [emailLevels]int
	var deviceCounts [categoricalLevels]int
	var paymentCounts [categoricalLevels]int
	var ipCounts [ipLevels]int
	var timeCounts [timeLevels]int
	visitEstimationPairs(accounts, config.MaxEstimationPairs, func(a, b model.Account) {
		if emailPresent(a.Email, b.Email) {
			emailCounts[emailAgreement(a.Email, b.Email, config)]++
		}
		if level, present := categoricalAgreement(a.DeviceID, b.DeviceID); present {
			deviceCounts[level]++
		}
		if level, present := categoricalAgreement(a.PaymentFingerprint, b.PaymentFingerprint); present {
			paymentCounts[level]++
		}
		if level, present := ipAgreement(a.IP, b.IP, config); present {
			ipCounts[level]++
		}
		if level, present := timeAgreement(a.CreatedAt, b.CreatedAt, config); present {
			timeCounts[level]++
		}
		result.pairCount++
	})
	copy(result.emailU[:], smoothDistribution(emailCounts[:], config.Smoothing))
	copy(result.deviceU[:], smoothDistribution(deviceCounts[:], config.Smoothing))
	copy(result.paymentU[:], smoothDistribution(paymentCounts[:], config.Smoothing))
	copy(result.ipU[:], smoothDistribution(ipCounts[:], config.Smoothing))
	copy(result.timeU[:], smoothDistribution(timeCounts[:], config.Smoothing))
	return result
}

func increment(counts map[string]int, value string) {
	if value != "" {
		counts[value]++
	}
}

func visitEstimationPairs(accounts []model.Account, limit int, visit func(model.Account, model.Account)) {
	if limit <= 0 {
		return
	}
	visited := 0
	for offset := 1; offset < len(accounts) && visited < limit; offset++ {
		for i := 0; i+offset < len(accounts) && visited < limit; i++ {
			visit(accounts[i], accounts[i+offset])
			visited++
		}
	}
}

func smoothDistribution(counts []int, smoothing float64) []float64 {
	smoothing = positiveSmoothing(smoothing)
	total := 0
	for _, count := range counts {
		total += count
	}
	denominator := float64(total) + smoothing*float64(len(counts))
	result := make([]float64, len(counts))
	for i, count := range counts {
		result[i] = (float64(count) + smoothing) / denominator
	}
	return result
}

func (s *Scorer) Score(a, b model.Account) float64 {
	return s.PairEvidence(a, b).Confidence
}

func (s *Scorer) RawScore(a, b model.Account) float64 {
	return s.PairEvidence(a, b).Raw
}

func (s *Scorer) PairEvidence(a, b model.Account) Evidence {
	result := Evidence{}
	if emailPresent(a.Email, b.Email) {
		emailLevel := emailAgreement(a.Email, b.Email, s.Config)
		emailU := s.model.emailU[emailLevel]
		if emailLevel == emailExact {
			emailU = s.valueProbability(s.model.emails, NormalizeEmail(a.Email))
		}
		result.Email = s.weight(s.Config.EmailM[emailLevel], emailU)
	}

	if level, present := categoricalAgreement(a.DeviceID, b.DeviceID); present {
		u := s.model.deviceU[level]
		if level == categoricalExact {
			u = s.valueProbability(s.model.devices, strings.TrimSpace(a.DeviceID))
		}
		result.Device = s.weight(s.Config.DeviceM[level], u)
	}
	if level, present := categoricalAgreement(a.PaymentFingerprint, b.PaymentFingerprint); present {
		u := s.model.paymentU[level]
		if level == categoricalExact {
			u = s.valueProbability(s.model.payments, strings.TrimSpace(a.PaymentFingerprint))
		}
		result.Payment = s.weight(s.Config.PaymentM[level], u)
	}
	if level, present := ipAgreement(a.IP, b.IP, s.Config); present {
		u := s.model.ipU[level]
		exact, high, mid := IPKeys(a.IP, s.Config)
		switch level {
		case ipExact:
			u = s.valueProbability(s.model.ipExact, exact)
		case ipHigh:
			u = s.valueProbability(s.model.ipHigh, high)
		case ipMid:
			u = s.valueProbability(s.model.ipMid, mid)
		}
		result.IP = s.weight(s.Config.IPM[level], u)
	}
	if level, present := timeAgreement(a.CreatedAt, b.CreatedAt, s.Config); present {
		result.Time = s.weight(s.Config.TimeM[level], s.model.timeU[level])
	}

	result.Raw = result.Email + result.Device + result.Payment + result.IP + result.Time
	result.Confidence = logistic(result.Raw)
	return result
}

func (s *Scorer) valueProbability(counts map[string]int, value string) float64 {
	smoothing := positiveSmoothing(s.Config.Smoothing)
	return (float64(counts[value]) + smoothing) /
		(float64(s.model.accountCount) + 2*smoothing)
}

func (s *Scorer) weight(m, u float64) float64 {
	smoothing := positiveSmoothing(s.Config.Smoothing)
	floor := smoothing / (float64(s.model.pairCount) + 2*smoothing)
	if m < floor {
		m = floor
	}
	if u < floor {
		u = floor
	}
	return math.Log(m / u)
}

func positiveSmoothing(value float64) float64 {
	if value > 0 {
		return value
	}
	return math.SmallestNonzeroFloat64
}

func logistic(value float64) float64 {
	if value >= 0 {
		z := math.Exp(-value)
		return 1 / (1 + z)
	}
	z := math.Exp(value)
	return z / (1 + z)
}

func emailAgreement(a, b string, config Config) int {
	a = NormalizeEmail(a)
	b = NormalizeEmail(b)
	if a != "" && a == b {
		return emailExact
	}
	similarity := normalizedLevenshtein(EmailLocal(a), EmailLocal(b))
	switch {
	case similarity >= config.EmailVeryHighThreshold:
		return emailVeryHigh
	case similarity >= config.EmailMediumThreshold:
		return emailMedium
	default:
		return emailLow
	}
}

func emailPresent(a, b string) bool {
	return NormalizeEmail(a) != "" && NormalizeEmail(b) != ""
}

func categoricalAgreement(a, b string) (int, bool) {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return 0, false
	}
	if a == b {
		return categoricalExact, true
	}
	return categoricalDifferent, true
}

func ipAgreement(a, b string, config Config) (int, bool) {
	aExact, aHigh, aMid := IPKeys(a, config)
	bExact, bHigh, bMid := IPKeys(b, config)
	if aExact == "" || bExact == "" {
		return 0, false
	}
	switch {
	case aExact == bExact:
		return ipExact, true
	case aHigh == bHigh:
		return ipHigh, true
	case aMid == bMid:
		return ipMid, true
	default:
		return ipDifferent, true
	}
}

func timeAgreement(a, b time.Time, config Config) (int, bool) {
	if a.IsZero() || b.IsZero() {
		return 0, false
	}
	delta := a.Sub(b)
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta <= config.TimeVeryClose:
		return timeVeryClose, true
	case delta <= config.TimeClose:
		return timeClose, true
	case delta <= config.TimeModerate:
		return timeModerate, true
	default:
		return timeFar, true
	}
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
