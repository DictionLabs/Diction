package pairing

import (
	"fmt"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// Link builds the diction://pair deep link encoded in the startup QR.
// publicURL is the self-hoster's PUBLIC_URL (scheme+host+port); when empty the
// link carries only the key and the app prompts for the URL after scanning.
func Link(publicURL, key string) string {
	q := url.Values{}
	if publicURL != "" {
		q.Set("url", publicURL)
	}
	q.Set("key", key)
	return "diction://pair?" + q.Encode()
}

// PrintQR renders the pairing deep link as a terminal QR code plus a
// copyable plain-text fallback. Printed with fmt (not log): log prefixes on
// each line would sit left of the quiet zone in `docker compose logs`, which
// is fine, but prefixes inside a wrapped matrix would break scanning.
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

// qrTerminalArt renders a QR matrix as UTF-8 half-blocks, two matrix rows per
// terminal line, with a 2-module quiet zone on all sides.
func qrTerminalArt(content string) (string, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}
	// Bitmap() includes the standard 4-module quiet zone; trim to 2 to keep the
	// matrix narrow enough for 80-column terminals.
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
