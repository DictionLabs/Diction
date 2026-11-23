package pairing

import (
	"fmt"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// Link builds the diction://pair deep link for the QR; without publicURL it carries only the key.
func Link(publicURL, key string) string {
	q := url.Values{}
	if publicURL != "" {
		q.Set("url", publicURL)
	}
	q.Set("key", key)
	return "diction://pair?" + q.Encode()
}

// PrintQR prints the pairing link as a QR plus plain text; fmt, not log, keeps rows unprefixed.
func PrintQR(publicURL, key string) {
	link := Link(publicURL, key)
	fmt.Println()
	fmt.Println("Pair the Diction app with this gateway. Scan in Diction > Self-Hosted > Scan to pair:")
	fmt.Println()
	if art, err := qrTerminalArt(link); err == nil {
		fmt.Print(art)
	}
	fmt.Println("Or open this link on your iPhone:")
	fmt.Println("  " + link)
	if publicURL == "" {
		fmt.Println("Tip: set PUBLIC_URL=https://your-host:port to embed the gateway address in the QR,")
		fmt.Println("so pairing fills in both the URL and the key.")
	}
	fmt.Println()
}

// qrTerminalArt renders a QR matrix as UTF-8 half-blocks with a 2-module quiet zone.
func qrTerminalArt(content string) (string, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}
	// Bitmap() includes a 4-module quiet zone; trim to 2 for 80-column terminals.
	bitmap := code.Bitmap()
	const trim = 2
	size := len(bitmap) - 2*trim
	at := func(x, y int) bool {
		return bitmap[y+trim][x+trim]
	}

	var b strings.Builder
	for y := 0; y < size; y += 2 {
		for x := 0; x < size; x++ {
			top := at(x, y)
			bottom := y+1 < size && at(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
