package similarity

import "time"

type LinkageRule string

const (
	CompleteLinkage      LinkageRule = "complete"
	AverageStrongLinkage LinkageRule = "average-strong"
)

// Config contains every tunable scoring and blocking parameter.
type Config struct {
	Smoothing           float64
	EvidenceScale       float64
	MergeThreshold      float64
	StrongPairThreshold float64
	LinkageRule         LinkageRule
	SingletonConfidence float64

	IPv4HighPrefixBits int
	IPv4MidPrefixBits  int
	IPv6HighPrefixBits int
	IPv6MidPrefixBits  int
	IPHighFactor       float64
	IPMidFactor        float64

	TimeEvidence float64
	TimeDecay    time.Duration

	EmailNGramSize       int
	MinEmailNGramMatches int
	ShortEmailRuneLength int
	MaxBlockSize         int
	MaxCandidates        int
}

func DefaultConfig() Config {
	return Config{
		Smoothing:           1,
		EvidenceScale:       0.75,
		MergeThreshold:      0.60,
		StrongPairThreshold: 0.75,
		LinkageRule:         CompleteLinkage,
		SingletonConfidence: 1,

		IPv4HighPrefixBits: 24,
		IPv4MidPrefixBits:  16,
		IPv6HighPrefixBits: 64,
		IPv6MidPrefixBits:  48,
		IPHighFactor:       0.45,
		IPMidFactor:        0.10,

		TimeEvidence: 0.25,
		TimeDecay:    7 * 24 * time.Hour,

		EmailNGramSize:       3,
		MinEmailNGramMatches: 2,
		ShortEmailRuneLength: 4,
		MaxBlockSize:         256,
		MaxCandidates:        4096,
	}
}
