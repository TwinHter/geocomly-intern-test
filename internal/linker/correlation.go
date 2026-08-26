package linker

import "sort"

// MergeStep records an accepted objective-improving agglomerative merge.
type MergeStep struct {
	LeftKey   int
	RightKey  int
	Gain      float64
	Objective float64
}

type graphCluster struct {
	active  bool
	members []int
}

func SignedEdge(score, neutralSimilarity float64) float64 {
	return score - neutralSimilarity
}

// Agglomerate merges the legal cluster pair with the highest positive gain.
// Node indices define deterministic tie-breaking.
func Agglomerate(edges [][]float64, cannotLink [][]bool) ([][]int, []MergeStep) {
	clusters := make([]graphCluster, len(edges))
	gains := make([][]float64, len(edges))
	blocked := make([][]bool, len(edges))
	for i := range edges {
		clusters[i] = graphCluster{active: true, members: []int{i}}
		gains[i] = append([]float64(nil), edges[i]...)
		blocked[i] = append([]bool(nil), cannotLink[i]...)
	}

	steps := make([]MergeStep, 0)
	objective := 0.0
	for {
		bestLeft, bestRight := -1, -1
		bestGain := 0.0
		for left := 0; left < len(clusters); left++ {
			if !clusters[left].active {
				continue
			}
			for right := left + 1; right < len(clusters); right++ {
				if !clusters[right].active || blocked[left][right] {
					continue
				}
				gain := gains[left][right]
				if gain > bestGain || (gain == bestGain && gain > 0 &&
					(bestLeft < 0 || left < bestLeft || (left == bestLeft && right < bestRight))) {
					bestLeft, bestRight, bestGain = left, right, gain
				}
			}
		}
		if bestLeft < 0 {
			break
		}

		objective += bestGain
		steps = append(steps, MergeStep{
			LeftKey:   clusters[bestLeft].members[0],
			RightKey:  clusters[bestRight].members[0],
			Gain:      bestGain,
			Objective: objective,
		})
		clusters[bestLeft].members = append(clusters[bestLeft].members, clusters[bestRight].members...)
		sort.Ints(clusters[bestLeft].members)
		clusters[bestRight].active = false

		for other := range clusters {
			if other == bestLeft || !clusters[other].active {
				continue
			}
			gain := gains[bestLeft][other] + gains[bestRight][other]
			isBlocked := blocked[bestLeft][other] || blocked[bestRight][other]
			gains[bestLeft][other], gains[other][bestLeft] = gain, gain
			blocked[bestLeft][other], blocked[other][bestLeft] = isBlocked, isBlocked
		}
	}

	groups := make([][]int, 0)
	for _, cluster := range clusters {
		if cluster.active {
			groups = append(groups, append([]int(nil), cluster.members...))
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	return groups, steps
}
