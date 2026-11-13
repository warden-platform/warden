package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitee.com/pam-platform/pam/server/internal/connector"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := connector.ServeLinuxHelper(ctx, os.Getenv("PAM_LINUX_HELPER_SOCKET")); err != nil {
		log.Fatal(err)
	}
}
