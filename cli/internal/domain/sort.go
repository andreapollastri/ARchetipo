package domain

import (
	"sort"
	"strconv"
	"strings"
)

// SortByPriorityThenCode ordina in-place per priorità (High > Medium > Low)
// e, a parità, per coda numerica del codice (es. US-2 < US-10).
func SortByPriorityThenCode(s []Spec) {
	rank := map[Priority]int{PriorityHigh: 0, PriorityMedium: 1, PriorityLow: 2}
	sort.SliceStable(s, func(i, j int) bool {
		ri, rj := rank[s[i].Priority], rank[s[j].Priority]
		if ri != rj {
			return ri < rj
		}
		return numericTail(s[i].Code) < numericTail(s[j].Code)
	})
}

func numericTail(code string) int {
	idx := strings.LastIndex(code, "-")
	if idx == -1 || idx == len(code)-1 {
		return 0
	}
	n, _ := strconv.Atoi(code[idx+1:])
	return n
}
