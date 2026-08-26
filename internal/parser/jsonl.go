package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"accountlinker/internal/model"
)

const maxLineBytes = 4 << 20

func LoadAccounts(path string) ([]model.Account, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open accounts: %w", err)
	}
	defer f.Close()

	var accounts []model.Account
	seen := make(map[string]struct{})
	err = ScanAccounts(f, func(account model.Account) error {
		if _, exists := seen[account.AccountID]; exists {
			return fmt.Errorf("duplicate account_id %q", account.AccountID)
		}
		seen[account.AccountID] = struct{}{}
		accounts = append(accounts, account)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	return accounts, nil
}

func LoadConstraints(path string) ([]model.Constraint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open constraints: %w", err)
	}
	defer f.Close()

	var constraints []model.Constraint
	err = scanJSONL(f, func(line []byte, lineNumber int) error {
		var constraint model.Constraint
		if err := json.Unmarshal(line, &constraint); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if constraint.AccountA == "" || constraint.AccountB == "" {
			return fmt.Errorf("line %d: account_a and account_b are required", lineNumber)
		}
		if constraint.Relation != "verified_distinct" {
			return fmt.Errorf("line %d: unsupported relation %q", lineNumber, constraint.Relation)
		}
		constraints = append(constraints, constraint)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read constraints: %w", err)
	}
	return constraints, nil
}

func ScanAccounts(r io.Reader, visit func(model.Account) error) error {
	return scanJSONL(r, func(line []byte, lineNumber int) error {
		var account model.Account
		if err := json.Unmarshal(line, &account); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if err := account.Validate(); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if err := visit(account); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		return nil
	})
}

func scanJSONL(r io.Reader, visit func([]byte, int) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := visit([]byte(line), lineNumber); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
