// Command flit is the meeting transcription tray app. The same executable
// also serves helper roles chosen by its first argument (see app.Run).
package main

import (
	"os"

	"github.com/motivated2die/flit/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:]))
}
