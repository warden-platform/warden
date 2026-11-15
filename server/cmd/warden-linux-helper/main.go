package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitee.com/pam-platform/warden/server/internal/connector"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := connector.ServeLinuxHelper(ctx, os.Getenv("WARDEN_LINUX_HELPER_SOCKET")); err != nil {
		log.Fatal(err)
	}
}
