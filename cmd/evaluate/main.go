package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"accountlinker/internal/linker"
	"accountlinker/internal/model"
	"accountlinker/internal/parser"
	"accountlinker/internal/similarity"
)

type truthFile struct {
	AccountToActor map[string]string `json:"account_to_actor"`
	FraudActorIDs  []string          `json:"fraud_actor_ids"`
}

type metrics struct {
	TP                       int                `json:"tp"`
	FP                       int                `json:"fp"`
	TN                       int                `json:"tn"`
	FN                       int                `json:"fn"`
	Accuracy                 float64            `json:"accuracy"`
	Precision                float64            `json:"precision"`
	Recall                   float64            `json:"recall"`
	F1                       float64            `json:"f1"`
	F2                       float64            `json:"f2"`
	FraudRingsRecovered      int                `json:"fraud_rings_recovered"`
	FraudRingsTotal          int                `json:"fraud_rings_total"`
	AffectedLegitimateActors int                `json:"affected_legitimate_actors"`
	BusinessCost             int                `json:"business_cost"`
	PredictedClusters        int                `json:"predicted_clusters"`
	SingletonClusters        int                `json:"singleton_clusters"`
	ConstraintViolations     int                `json:"constraint_violations"`
	StreamingCandidateRecall float64            `json:"streaming_candidate_recall,omitempty"`
	ActorClusterCounts       map[string]int     `json:"actor_cluster_counts"`
	FraudPairScores          map[string]float64 `json:"fraud_pair_scores,omitempty"`
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
	threshold := flag.Float64("threshold", -1, "override merge threshold")
	strongThreshold := flag.Float64("strong-threshold", -1, "override strong-pair threshold")
	linkage := flag.String("linkage", "", "complete or average-strong")
	flag.Parse()
	if *truthPath == "" || (*clustersPath == "" && *accountsPath == "") {
		return errors.New("--truth and either --clusters or --accounts are required")
	}
	if *clustersPath != "" && *accountsPath != "" {
		return errors.New("use only one of --clusters or --accounts")
	}

	var truth truthFile
	if err := decodeFile(*truthPath, &truth); err != nil {
		return fmt.Errorf("read truth: %w", err)
	}
	var predictions model.BatchOutput
	var constraints []model.Constraint
	var accounts []model.Account
	var config similarity.Config
	var err error
	if *clustersPath != "" {
		if err := decodeFile(*clustersPath, &predictions); err != nil {
			return fmt.Errorf("read clusters: %w", err)
		}
		if *constraintsPath != "" {
			constraints, err = parser.LoadConstraints(*constraintsPath)
			if err != nil {
				return err
			}
		}
	} else {
		if *constraintsPath == "" {
			return errors.New("--constraints is required with --accounts")
		}
		accounts, err = parser.LoadAccounts(*accountsPath)
		if err != nil {
			return err
		}
		constraints, err = parser.LoadConstraints(*constraintsPath)
		if err != nil {
			return err
		}
		config = similarity.DefaultConfig()
		if *threshold >= 0 {
			config.MergeThreshold = *threshold
		}
		if *strongThreshold >= 0 {
			config.StrongPairThreshold = *strongThreshold
		}
		if *linkage != "" {
			config.LinkageRule = similarity.LinkageRule(*linkage)
		}
		state, err := linker.Batch(accounts, constraints, config)
		if err != nil {
			return err
		}
		predictions = state.Output()
	}

	result, err := evaluate(predictions, truth, constraints)
	if err != nil {
		return err
	}
	if len(accounts) > 0 {
		result.StreamingCandidateRecall = candidateRecall(
			linker.StreamingCandidatePairs(accounts, config), truth.AccountToActor,
		)
		scorer := similarity.New(config)
		result.FraudPairScores = fraudPairScores(accounts, truth, scorer.Score)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func fraudPairScores(accounts []model.Account, truth truthFile, score func(model.Account, model.Account) float64) map[string]float64 {
	fraud := make(map[string]struct{}, len(truth.FraudActorIDs))
	for _, actorID := range truth.FraudActorIDs {
		fraud[actorID] = struct{}{}
	}
	byActor := make(map[string][]model.Account)
	for _, account := range accounts {
		actorID := truth.AccountToActor[account.AccountID]
		if _, exists := fraud[actorID]; exists {
			byActor[actorID] = append(byActor[actorID], account)
		}
	}
	result := make(map[string]float64)
	for actorID, actorAccounts := range byActor {
		sort.Slice(actorAccounts, func(i, j int) bool { return actorAccounts[i].AccountID < actorAccounts[j].AccountID })
		for i := 0; i < len(actorAccounts); i++ {
			for j := i + 1; j < len(actorAccounts); j++ {
				key := fmt.Sprintf("%s:%s/%s", actorID, actorAccounts[i].AccountID, actorAccounts[j].AccountID)
				result[key] = score(actorAccounts[i], actorAccounts[j])
			}
		}
	}
	return result
}

func decodeFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(destination)
}

func evaluate(predictions model.BatchOutput, truth truthFile, constraints []model.Constraint) (metrics, error) {
	clusterByAccount := make(map[string]string, len(truth.AccountToActor))
	clusterMembers := make(map[string][]string, len(predictions.Clusters))
	result := metrics{
		PredictedClusters:  len(predictions.Clusters),
		FraudRingsTotal:    len(truth.FraudActorIDs),
		ActorClusterCounts: make(map[string]int),
	}
	for _, cluster := range predictions.Clusters {
		if len(cluster.AccountIDs) == 1 {
			result.SingletonClusters++
		}
		for _, accountID := range cluster.AccountIDs {
			if _, duplicate := clusterByAccount[accountID]; duplicate {
				return metrics{}, fmt.Errorf("account %q appears in multiple predicted clusters", accountID)
			}
			if _, exists := truth.AccountToActor[accountID]; !exists {
				return metrics{}, fmt.Errorf("prediction contains unknown account %q", accountID)
			}
			clusterByAccount[accountID] = cluster.ClusterID
			clusterMembers[cluster.ClusterID] = append(clusterMembers[cluster.ClusterID], accountID)
		}
	}

	accountIDs := make([]string, 0, len(truth.AccountToActor))
	actorClusters := make(map[string]map[string]struct{})
	for accountID, actorID := range truth.AccountToActor {
		clusterID, exists := clusterByAccount[accountID]
		if !exists {
			return metrics{}, fmt.Errorf("truth account %q is missing from predictions", accountID)
		}
		accountIDs = append(accountIDs, accountID)
		if actorClusters[actorID] == nil {
			actorClusters[actorID] = make(map[string]struct{})
		}
		actorClusters[actorID][clusterID] = struct{}{}
	}
	sort.Strings(accountIDs)
	for actorID, clusters := range actorClusters {
		result.ActorClusterCounts[actorID] = len(clusters)
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
			actualSame := truth.AccountToActor[accountIDs[i]] == truth.AccountToActor[accountIDs[j]]
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

	fraudActors := make(map[string]struct{}, len(truth.FraudActorIDs))
	for _, actorID := range truth.FraudActorIDs {
		fraudActors[actorID] = struct{}{}
		if len(actorClusters[actorID]) == 1 {
			result.FraudRingsRecovered++
		}
	}
	affected := make(map[string]struct{})
	for _, members := range clusterMembers {
		actors := make(map[string]struct{})
		for _, accountID := range members {
			actors[truth.AccountToActor[accountID]] = struct{}{}
		}
		if len(actors) < 2 {
			continue
		}
		for actorID := range actors {
			if _, fraud := fraudActors[actorID]; !fraud {
				affected[actorID] = struct{}{}
			}
		}
	}
	result.AffectedLegitimateActors = len(affected)
	result.BusinessCost = 2000*(result.FraudRingsTotal-result.FraudRingsRecovered) +
		50*result.AffectedLegitimateActors

	total := result.TP + result.FP + result.TN + result.FN
	result.Accuracy = ratio(result.TP+result.TN, total)
	result.Precision = ratio(result.TP, result.TP+result.FP)
	result.Recall = ratio(result.TP, result.TP+result.FN)
	result.F1 = fbeta(result.Precision, result.Recall, 1)
	result.F2 = fbeta(result.Precision, result.Recall, 2)
	return result, nil
}

func candidateRecall(pairs [][2]string, truth map[string]string) float64 {
	found := 0
	for _, pair := range pairs {
		if truth[pair[0]] != "" && truth[pair[0]] == truth[pair[1]] {
			found++
		}
	}
	total := 0
	accountIDs := make([]string, 0, len(truth))
	for accountID := range truth {
		accountIDs = append(accountIDs, accountID)
	}
	for i := 0; i < len(accountIDs); i++ {
		for j := i + 1; j < len(accountIDs); j++ {
			if truth[accountIDs[i]] == truth[accountIDs[j]] {
				total++
			}
		}
	}
	return ratio(found, total)
}

func fbeta(precision, recall, beta float64) float64 {
	betaSquared := beta * beta
	denominator := betaSquared*precision + recall
	if denominator == 0 {
		return 0
	}
	return (1 + betaSquared) * precision * recall / denominator
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
