package linker

import "sort"

type dsu struct {
	parent  []int
	size    []int
	members [][]int
}

func newDSU(count int) *dsu {
	d := &dsu{
		parent:  make([]int, count),
		size:    make([]int, count),
		members: make([][]int, count),
	}
	for i := 0; i < count; i++ {
		d.parent[i] = i
		d.size[i] = 1
		d.members[i] = []int{i}
	}
	return d
}

func (d *dsu) find(x int) int {
	root := x
	for d.parent[root] != root {
		root = d.parent[root]
	}
	for d.parent[x] != x {
		next := d.parent[x]
		d.parent[x] = root
		x = next
	}
	return root
}

func (d *dsu) union(a, b int) int {
	ra := d.find(a)
	rb := d.find(b)
	if ra == rb {
		return ra
	}
	if d.size[ra] < d.size[rb] || (d.size[ra] == d.size[rb] && ra > rb) {
		ra, rb = rb, ra
	}
	d.parent[rb] = ra
	d.size[ra] += d.size[rb]
	d.members[ra] = append(d.members[ra], d.members[rb]...)
	sort.Ints(d.members[ra])
	d.members[rb] = nil
	return ra
}

func (d *dsu) componentMembers(x int) []int {
	return d.members[d.find(x)]
}
