// cmd/stockyard/main.go
package main

import (
	"fmt"
	"os"

	"github.com/obra/stockyard/pkg/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("stockyard %s\n", version.Version)
		os.Exit(0)
	}
	fmt.Println("stockyard - coding agent VM orchestrator")
}
