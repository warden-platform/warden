package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"gitee.com/pam-platform/warden/server/internal/config"
	"gitee.com/pam-platform/warden/server/internal/control"
	"gitee.com/pam-platform/warden/server/internal/storage/postgres"
)

func main() {
	cfg, err := config.LoadRuntime()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startupCancel()

	pool, err := postgres.ConnectPool(startupCtx)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}

	handler, err := control.NewHandler(pool)
	if err != nil {
		log.Fatalf("初始化策略决策失败: %v", err)
	}
	if err := handler.ValidateConnectorCredentials(startupCtx); err != nil {
		log.Fatalf("Connector 凭证自检失败: %v", err)
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	wrappedMux := control.LoggingMiddleware(control.CORSMiddleware(control.GzipMiddleware(mux)))
	appCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	handler.StartBackground(appCtx)

	port := strconv.Itoa(cfg.Server.Port)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           wrappedMux,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("warden-control 启动于 :%s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务错误: %v", err)
		}
	}()

	<-appCtx.Done()

	log.Println("正在关闭服务...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("关闭失败: %v", err)
	}
}
