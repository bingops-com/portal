package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bingops-com/portal/internal/auth"
	"github.com/bingops-com/portal/internal/cache"
	"github.com/bingops-com/portal/internal/config"
	"github.com/bingops-com/portal/internal/providers"
	"github.com/bingops-com/portal/internal/server"
	"github.com/bingops-com/portal/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	store := config.NewStore(env("PORTAL_CONFIG", "config/portal.yaml"), env("PORTAL_DATA", "data"), providers.Known)
	if _, _, err := store.Effective(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	srv := &server.Server{
		Store:     store,
		Deps:      &providers.Deps{Kube: providers.NewKubeClients()},
		Cache:     cache.New(),
		Assets:    web.Assets(),
		ReadOnly:  os.Getenv("PORTAL_READONLY") == "true",
		StatePath: filepath.Join(env("PORTAL_DATA", "data"), "readouts.json"),
	}
	if hosts := os.Getenv("PORTAL_INTERNAL_HOSTS"); hosts != "" {
		providers.SetInternalHosts(strings.Split(hosts, ","))
	}
	if issuer := os.Getenv("PORTAL_OIDC_ISSUER"); issuer != "" {
		login, err := auth.New(auth.Config{
			Issuer:    issuer,
			ClientID:  os.Getenv("PORTAL_OIDC_CLIENT_ID"),
			Group:     os.Getenv("PORTAL_OIDC_GROUP"),
			PublicURL: os.Getenv("PORTAL_PUBLIC_URL"),
		}, env("PORTAL_DATA", "data"))
		if err != nil {
			slog.Error("invalid login configuration", "error", err)
			os.Exit(1)
		}
		srv.Auth = login
	}
	httpServer := &http.Server{
		Addr:              env("PORTAL_ADDR", ":3000"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()

	slog.Info("portal listening", "addr", httpServer.Addr, "readOnly", srv.ReadOnly, "loginRequired", srv.Auth != nil)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
