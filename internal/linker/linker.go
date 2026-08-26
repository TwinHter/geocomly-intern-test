package linker

import (
	"fmt"
	"sort"

	"accountlinker/internal/constraint"
	"accountlinker/internal/model"
	"accountlinker/internal/similarity"
)

type pairKey struct {
	a int
	b int
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
	edgeCache      map[pairKey]float64
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

	scorer := similarity.New(config)
	distinct := constraint.New(constraints)
	edges := make([][]float64, len(ordered))
	cannotLink := make([][]bool, len(ordered))
	edgeCache := make(map[pairKey]float64, len(ordered)*(len(ordered)-1)/2)
	for i := range ordered {
		edges[i] = make([]float64, len(ordered))
		cannotLink[i] = make([]bool, len(ordered))
		for j := 0; j < i; j++ {
			edge := SignedEdge(scorer.Score(ordered[i], ordered[j]), config.NeutralSimilarity)
			edges[i][j], edges[j][i] = edge, edge
			edgeCache[newPairKey(i, j)] = edge
			blocked := distinct.VerifiedDistinct(ordered[i].AccountID, ordered[j].AccountID)
			cannotLink[i][j], cannotLink[j][i] = blocked, blocked
		}
	}
	groups, _ := Agglomerate(edges, cannotLink)

	state := &State{
		config:         config,
		scorer:         scorer,
		constraints:    distinct,
		accounts:       ordered,
		accountByID:    make(map[string]int, len(ordered)),
		accountCluster: make(map[string]string, len(ordered)),
		clusters:       make(map[string]*Cluster, len(groups)),
		index:          newCandidateIndex(config),
		edgeCache:      edgeCache,
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

func newPairKey(a, b int) pairKey {
	if a > b {
		a, b = b, a
	}
	return pairKey{a: a, b: b}
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

	accountIndex := len(s.accounts)
	computedEdges := make(map[pairKey]float64)
	var best *Cluster
	bestGain := 0.0
	bestConfidence := s.config.SingletonConfidence
	for _, cluster := range orderedClusters {
		gain, confidence, valid := s.insertionGain(account, accountIndex, cluster, computedEdges)
		if !valid || gain <= 0 {
			continue
		}
		if gain > bestGain || (gain == bestGain && (best == nil || cluster.Order < best.Order)) {
			best = cluster
			bestGain = gain
			bestConfidence = confidence
		}
	}

	s.accounts = append(s.accounts, account)
	s.accountByID[account.AccountID] = accountIndex
	if best == nil {
		id := fmt.Sprintf("c%d", s.nextCluster)
		best = &Cluster{ID: id, Order: s.nextCluster, Members: []int{accountIndex}}
		s.clusters[id] = best
		s.nextCluster++
	} else {
		best.Members = append(best.Members, accountIndex)
	}
	s.accountCluster[account.AccountID] = best.ID
	s.index.add(account, accountIndex)
	for key, edge := range computedEdges {
		s.edgeCache[key] = edge
	}

	return model.StreamOutput{
		AccountID:  account.AccountID,
		ClusterID:  best.ID,
		Confidence: bestConfidence,
	}, nil
}

func (s *State) insertionGain(account model.Account, accountIndex int, cluster *Cluster, cache map[pairKey]float64) (float64, float64, bool) {
	gain := 0.0
	similaritySum := 0.0
	for _, member := range cluster.Members {
		existing := s.accounts[member]
		if s.constraints.VerifiedDistinct(account.AccountID, existing.AccountID) {
			return 0, 0, false
		}
		score := s.scorer.Score(account, existing)
		edge := SignedEdge(score, s.config.NeutralSimilarity)
		cache[newPairKey(accountIndex, member)] = edge
		gain += edge
		similaritySum += score
	}
	return gain, similaritySum / float64(len(cluster.Members)), true
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
	total := 0.0
	pairs := 0
	for i := 0; i < len(cluster.Members); i++ {
		for j := i + 1; j < len(cluster.Members); j++ {
			edge, exists := s.edgeCache[newPairKey(cluster.Members[i], cluster.Members[j])]
			if !exists {
				edge = SignedEdge(
					s.scorer.Score(s.accounts[cluster.Members[i]], s.accounts[cluster.Members[j]]),
					s.config.NeutralSimilarity,
				)
			}
			total += edge + s.config.NeutralSimilarity
			pairs++
		}
	}
	return total / float64(pairs)
}
