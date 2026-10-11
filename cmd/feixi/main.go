package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
)

func main() {
	log := slog.Default()

	fs := flag.NewFlagSet("feixi", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "打印版本后退出")
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Println("飞檄", version)
		return
	}

	args := fs.Args()
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	ctx, cancel := signalContext()
	defer cancel()

	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, log)
	case "prune":
		err = runPrune(ctx, log)
	default:
		err = fmt.Errorf("未知命令 %q（可用：serve、prune）", cmd)
	}
	if err != nil && err != context.Canceled {
		log.Error("退出", "err", err)
		os.Exit(1)
	}
}
