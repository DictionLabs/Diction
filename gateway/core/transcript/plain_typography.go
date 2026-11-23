package transcript

import "strings"

var plainTypography = strings.NewReplacer(
	"‐", "-",
	"‑", "-",
	" ", " ",
	" ", " ",
	" ", " ",
)

func PlainTypography(s string) string {
	return plainTypography.Replace(s)
}
