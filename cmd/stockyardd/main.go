// cmd/stockyardd/main.go
package main

import (
	"fmt"
	"os"

	"github.com/obra/stockyard/pkg/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("stockyardd %s\n", version.Version)
		os.Exit(0)
	}
	fmt.Println("stockyardd - stockyard daemon")
}
