package ui

import "fmt"

// cfHTML wraps an HTML fragment in the clipboard's "HTML Format": a header
// with the byte offsets of the document and the fragment in it (UTF-8).
func cfHTML(fragment string) string {
	const header = "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
	pre := "<html><body><!--StartFragment-->"
	post := "<!--EndFragment--></body></html>"
	h := len(fmt.Sprintf(header, 0, 0, 0, 0))
	start := h + len(pre)
	end := start + len(fragment)
	return fmt.Sprintf(header, h, end+len(post), start, end) + pre + fragment + post
}
