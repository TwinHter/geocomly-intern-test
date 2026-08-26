package constraint

import "accountlinker/internal/model"

type Set struct {
	distinct map[string]map[string]struct{}
}

func New(constraints []model.Constraint) *Set {
	set := &Set{distinct: make(map[string]map[string]struct{})}
	for _, c := range constraints {
		if c.Relation != "verified_distinct" || c.AccountA == c.AccountB {
			continue
		}
		set.add(c.AccountA, c.AccountB)
		set.add(c.AccountB, c.AccountA)
	}
	return set
}

func (s *Set) add(a, b string) {
	if s.distinct[a] == nil {
		s.distinct[a] = make(map[string]struct{})
	}
	s.distinct[a][b] = struct{}{}
}

func (s *Set) VerifiedDistinct(a, b string) bool {
	_, exists := s.distinct[a][b]
	return exists
}
