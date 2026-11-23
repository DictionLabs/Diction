package vocab

func lengthRatioExceeds(a, b string, limit float64) bool {
	la, lb := len([]rune(a)), len([]rune(b))
	longest, diff := la, la-lb
	if lb > longest {
		longest = lb
	}
	if diff < 0 {
		diff = -diff
	}
	if longest == 0 {
		return false
	}
	return float64(diff)/float64(longest) > limit
}

func normalizedLevenshtein(a, b string) float64 {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 1
	}
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min3(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	longest := len(ra)
	if len(rb) > longest {
		longest = len(rb)
	}
	return float64(previous[len(rb)]) / float64(longest)
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
