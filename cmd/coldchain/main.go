package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"coldchain-route-ledger/internal/httpapi"
	"coldchain-route-ledger/internal/repository"
	"coldchain-route-ledger/internal/service"
)

func main() {
	address := flag.String("addr", ":8080", "HTTP 监听地址")
	ledgerPath := flag.String("ledger", "./data/coldchain-ledger.json", "本地 JSON 账本路径")
	selfcheck := flag.Bool("selfcheck", false, "执行内存账本自检并退出")
	flag.Parse()

	if *selfcheck {
		if err := service.RunSelfCheck(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "自检失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("冷链配送账本自检通过")
		return
	}

	store, err := repository.Open(*ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开本地账本失败: %v\n", err)
		os.Exit(1)
	}
	svc := service.New(store)
	server := &http.Server{Addr: *address, Handler: httpapi.NewServer(svc), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	stop, stopSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "HTTP 服务停止: %v\n", err)
			os.Exit(1)
		}
	case <-stop.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP 服务优雅退出失败: %v\n", err)
			os.Exit(1)
		}
	}
}
