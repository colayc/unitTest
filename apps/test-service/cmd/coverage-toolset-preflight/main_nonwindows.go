//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"runtime"
)

func main() {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"schemaVersion": 1,
		"platform":      runtime.GOOS,
		"architecture":  runtime.GOARCH,
		"status":        "unavailable",
	})
}
