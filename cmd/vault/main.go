// vault — shared Claude Code sessions in a shared folder.
package main

import (
	"os"

	"github.com/shashb27/vault/internal/vault"
)

func main() { os.Exit(vault.Main(os.Args[1:])) }
