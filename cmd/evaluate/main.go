package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"accountlinker/internal/linker"
	"accountlinker/internal/model"
	"accountlinker/internal/parser"
	"accountlinker/internal/similarity"
)

type truthFile struct {
	AccountToActor map[string]string `json:"account_to_actor"`
}

type metrics struct {
	TP                   int     `json:"tp"`
	FP                   int     `json:"fp"`
	TN                   int     `json:"tn"`
	FN                   int     `json:"fn"`
	Accuracy             float64 `json:"accuracy"`
	Precision            float64 `json:"precision"`
	Recall               float64 `json:"recall"`
	F1                   float64 `json:"f1"`
	PredictedClusters    int     `json:"predicted_clusters"`
	SingletonClusters    int     `json:"singleton_clusters"`
	ConstraintViolations int     `json:"constraint_violations"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "evaluate:", err)
		os.Exit(1)
	}
}

func run() error {
	clustersPath := flag.String("clusters", "", "predicted clusters JSON path")
	accountsPath := flag.String("accounts", "", "accounts JSONL path (alternative to --clusters)")
	constraintsPath := flag.String("constraints", "", "constraints JSONL path")
	truthPath := flag.String("truth", "", "ground-truth JSON path")
	neutral := flag.Float64("neutral", 0, "override neutral similarity with --accounts")
	flag.Parse()
	if *truthPath == "" || (*clustersPath == "" && *accountsPath == "") {
		return errors.New("--truth and either --clusters or --accounts are required")
	}

	var predictions model.BatchOutput
	var constraints []model.Constraint
	var err error
	if *clustersPath != "" {
		if err := decodeFile(*clustersPath, &predictions); err != nil {
			return fmt.Errorf("read clusters: %w", err)
		}
	} else {
		if *constraintsPath == "" {
			return errors.New("--constraints is required with --accounts")
		}
		accounts, err := parser.LoadAccounts(*accountsPath)
		if err != nil {
			return err
		}
		constraints, err = parser.LoadConstraints(*constraintsPath)
		if err != nil {
			return err
		}
		config := similarity.DefaultConfig()
		if flagWasSet("neutral") {
			config.NeutralSimilarity = *neutral
		}
		state, err := linker.Batch(accounts, constraints, config)
		if err != nil {
			return err
		}
		predictions = state.Output()
	}

	var truth truthFile
	if err := decodeFile(*truthPath, &truth); err != nil {
		return fmt.Errorf("read truth: %w", err)
	}
	if *clustersPath != "" && *constraintsPath != "" {
		constraints, err = parser.LoadConstraints(*constraintsPath)
		if err != nil {
			return err
		}
	}
	result, err := evaluate(predictions, truth.AccountToActor, constraints)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func flagWasSet(name string) bool {
	set := false
	flag.Visit(func(value *flag.Flag) {
		if value.Name == name {
			set = true
		}
	})
	return set
}

func decodeFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(destination)
}

func evaluate(predictions model.BatchOutput, truth map[string]string, constraints []model.Constraint) (metrics, error) {
	clusterByAccount := make(map[string]string, len(truth))
	result := metrics{PredictedClusters: len(predictions.Clusters)}
	for _, cluster := range predictions.Clusters {
		if len(cluster.AccountIDs) == 1 {
			result.SingletonClusters++
		}
		for _, accountID := range cluster.AccountIDs {
			if _, duplicate := clusterByAccount[accountID]; duplicate {
				return metrics{}, fmt.Errorf("account %q appears in multiple predicted clusters", accountID)
			}
			clusterByAccount[accountID] = cluster.ClusterID
		}
	}

	accountIDs := make([]string, 0, len(truth))
	for accountID := range truth {
		if _, exists := clusterByAccount[accountID]; !exists {
			return metrics{}, fmt.Errorf("truth account %q is missing from predictions", accountID)
		}
		accountIDs = append(accountIDs, accountID)
	}
	if len(clusterByAccount) != len(truth) {
		return metrics{}, fmt.Errorf("predictions contain %d accounts but truth contains %d", len(clusterByAccount), len(truth))
	}
	for _, constraint := range constraints {
		if constraint.Relation == "verified_distinct" &&
			clusterByAccount[constraint.AccountA] != "" &&
			clusterByAccount[constraint.AccountA] == clusterByAccount[constraint.AccountB] {
			result.ConstraintViolations++
		}
	}

	for i := 0; i < len(accountIDs); i++ {
		for j := i + 1; j < len(accountIDs); j++ {
			predictedSame := clusterByAccount[accountIDs[i]] == clusterByAccount[accountIDs[j]]
			actualSame := truth[accountIDs[i]] == truth[accountIDs[j]]
			switch {
			case predictedSame && actualSame:
				result.TP++
			case predictedSame:
				result.FP++
			case actualSame:
				result.FN++
			default:
				result.TN++
			}
		}
	}

	total := result.TP + result.FP + result.TN + result.FN
	result.Accuracy = ratio(result.TP+result.TN, total)
	result.Precision = ratio(result.TP, result.TP+result.FP)
	result.Recall = ratio(result.TP, result.TP+result.FN)
	if result.Precision+result.Recall > 0 {
		result.F1 = 2 * result.Precision * result.Recall / (result.Precision + result.Recall)
	}
	return result, nil
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
