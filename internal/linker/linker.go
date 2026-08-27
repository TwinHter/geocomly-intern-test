package linker

import (
	"fmt"
	"sort"

	"accountlinker/internal/constraint"
	"accountlinker/internal/model"
	"accountlinker/internal/similarity"
)

type scoredPair struct {
	a     int
	b     int
	score float64
}

type Cluster struct {
	ID      string
	Order   int
	Members []int
}

type State struct {
	config         similarity.Config
	scorer         similarity.Scorer
	constraints    *constraint.Set
	accounts       []model.Account
	accountByID    map[string]int
	accountCluster map[string]string
	clusters       map[string]*Cluster
	index          *candidateIndex
	nextCluster    int
}

func Batch(accounts []model.Account, constraints []model.Constraint, config similarity.Config) (*State, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	ordered := append([]model.Account(nil), accounts...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].AccountID < ordered[j].AccountID
	})
	for i := range ordered {
		if err := ordered[i].Validate(); err != nil {
			return nil, err
		}
		if i > 0 && ordered[i-1].AccountID == ordered[i].AccountID {
			return nil, fmt.Errorf("duplicate account_id %q", ordered[i].AccountID)
		}
	}

	scorer := similarity.New(config)
	distinct := constraint.New(constraints)
	scores := make([][]float64, len(ordered))
	pairs := make([]scoredPair, 0, len(ordered)*(len(ordered)-1)/2)
	for i := range ordered {
		scores[i] = make([]float64, len(ordered))
		for j := 0; j < i; j++ {
			score := scorer.Score(ordered[j], ordered[i])
			scores[i][j], scores[j][i] = score, score
			pairs = append(pairs, scoredPair{a: j, b: i, score: score})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if ordered[pairs[i].a].AccountID != ordered[pairs[j].a].AccountID {
			return ordered[pairs[i].a].AccountID < ordered[pairs[j].a].AccountID
		}
		return ordered[pairs[i].b].AccountID < ordered[pairs[j].b].AccountID
	})

	components := newDSU(len(ordered))
	if config.LinkageRule == similarity.AverageStrongLinkage {
		agglomerateAverageStrong(components, scores, ordered, distinct, config)
	} else {
		agglomerateComplete(components, pairs, scores, ordered, distinct, config.MergeThreshold)
	}

	groups := make([][]int, 0)
	for i := range ordered {
		if components.find(i) == i {
			members := append([]int(nil), components.members[i]...)
			sort.Slice(members, func(a, b int) bool {
				return ordered[members[a]].AccountID < ordered[members[b]].AccountID
			})
			groups = append(groups, members)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		return ordered[groups[i][0]].AccountID < ordered[groups[j][0]].AccountID
	})

	state := &State{
		config:         config,
		scorer:         scorer,
		constraints:    distinct,
		accounts:       ordered,
		accountByID:    make(map[string]int, len(ordered)),
		accountCluster: make(map[string]string, len(ordered)),
		clusters:       make(map[string]*Cluster, len(groups)),
		index:          newCandidateIndex(config),
		nextCluster:    len(groups) + 1,
	}
	for i, account := range ordered {
		state.accountByID[account.AccountID] = i
		state.index.add(account, i)
	}
	for i, members := range groups {
		id := fmt.Sprintf("c%d", i+1)
		cluster := &Cluster{ID: id, Order: i + 1, Members: members}
		state.clusters[id] = cluster
		for _, member := range members {
			state.accountCluster[ordered[member].AccountID] = id
		}
	}
	return state, nil
}

func validateConfig(config similarity.Config) error {
	if config.MergeThreshold < 0 || config.MergeThreshold > 1 {
		return fmt.Errorf("merge threshold must be in [0,1]")
	}
	if config.LinkageRule != similarity.CompleteLinkage && config.LinkageRule != similarity.AverageStrongLinkage {
		return fmt.Errorf("unsupported linkage rule %q", config.LinkageRule)
	}
	if config.LinkageRule == similarity.AverageStrongLinkage &&
		(config.StrongPairThreshold < config.MergeThreshold || config.StrongPairThreshold > 1) {
		return fmt.Errorf("strong-pair threshold must be between merge threshold and 1")
	}
	return nil
}

func agglomerateComplete(components *dsu, pairs []scoredPair, scores [][]float64, accounts []model.Account, constraints *constraint.Set, threshold float64) {
	for _, pair := range pairs {
		if pair.score < threshold {
			break
		}
		ra := components.find(pair.a)
		rb := components.find(pair.b)
		if ra == rb {
			continue
		}
		if canMergeComplete(components.members[ra], components.members[rb], scores, accounts, constraints, threshold) {
			components.union(ra, rb)
		}
	}
}

func canMergeComplete(aMembers, bMembers []int, scores [][]float64, accounts []model.Account, constraints *constraint.Set, threshold float64) bool {
	for _, ai := range aMembers {
		for _, bi := range bMembers {
			if constraints.VerifiedDistinct(accounts[ai].AccountID, accounts[bi].AccountID) {
				return false
			}
			if scores[ai][bi] < threshold {
				return false
			}
		}
	}
	return true
}

func agglomerateAverageStrong(components *dsu, scores [][]float64, accounts []model.Account, constraints *constraint.Set, config similarity.Config) {
	for {
		roots := make([]int, 0)
		for i := range accounts {
			if components.find(i) == i {
				roots = append(roots, i)
			}
		}
		bestA, bestB := -1, -1
		bestAverage := -1.0
		for i := 0; i < len(roots); i++ {
			for j := i + 1; j < len(roots); j++ {
				a, b := roots[i], roots[j]
				average, strongest, valid := crossClusterStats(
					components.members[a], components.members[b], scores, accounts, constraints,
				)
				if !valid || average < config.MergeThreshold || strongest < config.StrongPairThreshold {
					continue
				}
				if average > bestAverage || (average == bestAverage &&
					(bestA < 0 || a < bestA || (a == bestA && b < bestB))) {
					bestA, bestB, bestAverage = a, b, average
				}
			}
		}
		if bestA < 0 {
			return
		}
		components.union(bestA, bestB)
	}
}

func crossClusterStats(aMembers, bMembers []int, scores [][]float64, accounts []model.Account, constraints *constraint.Set) (float64, float64, bool) {
	total := 0.0
	strongest := 0.0
	pairs := 0
	for _, ai := range aMembers {
		for _, bi := range bMembers {
			if constraints.VerifiedDistinct(accounts[ai].AccountID, accounts[bi].AccountID) {
				return 0, 0, false
			}
			score := scores[ai][bi]
			total += score
			pairs++
			if score > strongest {
				strongest = score
			}
		}
	}
	return total / float64(pairs), strongest, true
}

func (s *State) Add(account model.Account) (model.StreamOutput, error) {
	if err := account.Validate(); err != nil {
		return model.StreamOutput{}, err
	}
	if _, exists := s.accountByID[account.AccountID]; exists {
		return model.StreamOutput{}, fmt.Errorf("duplicate account_id %q", account.AccountID)
	}

	candidateClusters := make(map[string]*Cluster)
	for _, candidate := range s.index.candidates(account) {
		id := s.accountCluster[s.accounts[candidate].AccountID]
		candidateClusters[id] = s.clusters[id]
	}
	orderedClusters := make([]*Cluster, 0, len(candidateClusters))
	for _, cluster := range candidateClusters {
		orderedClusters = append(orderedClusters, cluster)
	}
	sort.Slice(orderedClusters, func(i, j int) bool {
		return orderedClusters[i].Order < orderedClusters[j].Order
	})

	var best *Cluster
	bestScore := -1.0
	for _, cluster := range orderedClusters {
		score, valid := s.clusterScore(account, cluster)
		if !valid {
			continue
		}
		if score > bestScore || (score == bestScore && (best == nil || cluster.Order < best.Order)) {
			best = cluster
			bestScore = score
		}
	}

	accountIndex := len(s.accounts)
	s.accounts = append(s.accounts, account)
	s.accountByID[account.AccountID] = accountIndex
	if best == nil {
		id := fmt.Sprintf("c%d", s.nextCluster)
		best = &Cluster{ID: id, Order: s.nextCluster, Members: []int{accountIndex}}
		s.clusters[id] = best
		s.nextCluster++
		bestScore = s.config.SingletonConfidence
	} else {
		best.Members = append(best.Members, accountIndex)
	}
	s.accountCluster[account.AccountID] = best.ID
	s.index.add(account, accountIndex)

	return model.StreamOutput{
		AccountID:  account.AccountID,
		ClusterID:  best.ID,
		Confidence: bestScore,
	}, nil
}

func (s *State) clusterScore(account model.Account, cluster *Cluster) (float64, bool) {
	minimum := 1.0
	total := 0.0
	strongest := 0.0
	for _, member := range cluster.Members {
		existing := s.accounts[member]
		if s.constraints.VerifiedDistinct(account.AccountID, existing.AccountID) {
			return 0, false
		}
		score := s.scorer.Score(account, existing)
		if s.config.LinkageRule == similarity.CompleteLinkage && score < s.config.MergeThreshold {
			return 0, false
		}
		total += score
		if score > strongest {
			strongest = score
		}
		if score < minimum {
			minimum = score
		}
	}
	if s.config.LinkageRule == similarity.AverageStrongLinkage {
		average := total / float64(len(cluster.Members))
		if average < s.config.MergeThreshold || strongest < s.config.StrongPairThreshold {
			return 0, false
		}
		return average, true
	}
	return minimum, true
}

func (s *State) Output() model.BatchOutput {
	clusters := make([]*Cluster, 0, len(s.clusters))
	for _, cluster := range s.clusters {
		clusters = append(clusters, cluster)
	}
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Order < clusters[j].Order
	})
	result := model.BatchOutput{Clusters: make([]model.ClusterOutput, 0, len(clusters))}
	for _, cluster := range clusters {
		accountIDs := make([]string, 0, len(cluster.Members))
		for _, member := range cluster.Members {
			accountIDs = append(accountIDs, s.accounts[member].AccountID)
		}
		sort.Strings(accountIDs)
		result.Clusters = append(result.Clusters, model.ClusterOutput{
			ClusterID:  cluster.ID,
			AccountIDs: accountIDs,
			Confidence: s.clusterConfidence(cluster),
		})
	}
	return result
}

func (s *State) clusterConfidence(cluster *Cluster) float64 {
	if len(cluster.Members) < 2 {
		return s.config.SingletonConfidence
	}
	minimum := 1.0
	total := 0.0
	pairs := 0
	for i := 0; i < len(cluster.Members); i++ {
		for j := i + 1; j < len(cluster.Members); j++ {
			score := s.scorer.Score(s.accounts[cluster.Members[i]], s.accounts[cluster.Members[j]])
			total += score
			pairs++
			if score < minimum {
				minimum = score
			}
		}
	}
	if s.config.LinkageRule == similarity.AverageStrongLinkage {
		return total / float64(pairs)
	}
	return minimum
}
