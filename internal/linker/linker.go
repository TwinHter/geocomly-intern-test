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
	scorer         *similarity.Scorer
	constraints    *constraint.Set
	accounts       []model.Account
	accountByID    map[string]int
	accountCluster map[string]string
	clusters       map[string]*Cluster
	index          *candidateIndex
	nextCluster    int
}

func Batch(accounts []model.Account, constraints []model.Constraint, config similarity.Config) (*State, error) {
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

	scorer := similarity.New(config, ordered)
	distinct := constraint.New(constraints)
	index := newCandidateIndex(config)
	pairs := make([]scoredPair, 0)
	for i, account := range ordered {
		for _, candidate := range index.candidates(account) {
			score := scorer.RawScore(ordered[candidate], account)
			if score >= config.LinkEvidenceThreshold {
				pairs = append(pairs, scoredPair{a: candidate, b: i, score: score})
			}
		}
		index.add(account, i)
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
	for _, pair := range pairs {
		ra := components.find(pair.a)
		rb := components.find(pair.b)
		if ra == rb {
			continue
		}
		if canMerge(components.members[ra], components.members[rb], ordered, scorer, distinct, config.LinkEvidenceThreshold) {
			components.union(ra, rb)
		}
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

func canMerge(aMembers, bMembers []int, accounts []model.Account, scorer *similarity.Scorer, constraints *constraint.Set, threshold float64) bool {
	for _, ai := range aMembers {
		for _, bi := range bMembers {
			if constraints.VerifiedDistinct(accounts[ai].AccountID, accounts[bi].AccountID) {
				return false
			}
			if scorer.RawScore(accounts[ai], accounts[bi]) < threshold {
				return false
			}
		}
	}
	return true
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
	for _, member := range cluster.Members {
		existing := s.accounts[member]
		if s.constraints.VerifiedDistinct(account.AccountID, existing.AccountID) {
			return 0, false
		}
		evidence := s.scorer.PairEvidence(account, existing)
		if evidence.Raw < s.config.LinkEvidenceThreshold {
			return 0, false
		}
		score := evidence.Confidence
		if score < minimum {
			minimum = score
		}
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
	for i := 0; i < len(cluster.Members); i++ {
		for j := i + 1; j < len(cluster.Members); j++ {
			score := s.scorer.Score(s.accounts[cluster.Members[i]], s.accounts[cluster.Members[j]])
			if score < minimum {
				minimum = score
			}
		}
	}
	return minimum
}
