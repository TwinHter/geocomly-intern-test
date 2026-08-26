package model

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type Account struct {
	AccountID          string    `json:"account_id"`
	Email              string    `json:"email"`
	DeviceID           string    `json:"device_id"`
	IP                 string    `json:"ip"`
	PaymentFingerprint string    `json:"payment_fingerprint"`
	CreatedAt          time.Time `json:"created_at"`
}

func (a Account) Validate() error {
	if strings.TrimSpace(a.AccountID) == "" {
		return fmt.Errorf("account_id is required")
	}
	if a.CreatedAt.IsZero() {
		return fmt.Errorf("account %q: created_at is required", a.AccountID)
	}
	if a.IP != "" {
		if _, err := netip.ParseAddr(strings.TrimSpace(a.IP)); err != nil {
			return fmt.Errorf("account %q: invalid IP address %q", a.AccountID, a.IP)
		}
	}
	return nil
}

type Constraint struct {
	AccountA string `json:"account_a"`
	AccountB string `json:"account_b"`
	Relation string `json:"relation"`
}

type ClusterOutput struct {
	ClusterID  string   `json:"cluster_id"`
	AccountIDs []string `json:"account_ids"`
	Confidence float64  `json:"confidence"`
}

type BatchOutput struct {
	Clusters []ClusterOutput `json:"clusters"`
}

type StreamOutput struct {
	AccountID  string  `json:"account_id"`
	ClusterID  string  `json:"cluster_id"`
	Confidence float64 `json:"confidence"`
}
