package main

import (
	"os"

	"github.com/JimChengLin/zhihu-tui/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
