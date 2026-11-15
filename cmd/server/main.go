package main

import (
	"context"
	"errors"
	"expensemother/internal/adapters/auth"
	"expensemother/internal/adapters/httpapi"
	"expensemother/internal/adapters/ocr"
	"expensemother/internal/adapters/postgres"
	"expensemother/internal/adapters/receiptfiles"
	"expensemother/internal/adapters/telegram"
	app "expensemother/internal/application"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	secret := os.Getenv("AUTH_SECRET")
	if len(secret) < 32 || strings.HasPrefix(secret, "replace-") {
		return errors.New("AUTH_SECRET must contain at least 32 bytes")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL required")
	}
	st, e := postgres.Open(ctx, dsn)
	if e != nil {
		return e
	}
	defer st.Pool.Close()
	if e = st.Migrate(ctx); e != nil {
		return e
	}
	service := app.New(st)
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	client := telegram.NewClient(botToken)
	if botToken != "" {
		service.Telegram = client
	}
	authService := auth.New(service, secret, botToken, os.Getenv("TELEGRAM_CLIENT_ID"))
	files := &receiptfiles.Files{Root: env("RECEIPT_DIR", "data/receipts"), Service: service}
	engine := ocr.Tesseract{Binary: env("TESSERACT_BIN", "tesseract")}
	origins := map[string]bool{}
	for _, v := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			origins[v] = true
		}
	}
	api := &httpapi.Server{Service: service, Auth: authService, Files: files, OCR: engine, Origins: origins}
	server := &http.Server{Addr: env("HTTP_ADDR", ":7008"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	go func() { slog.Info("HTTP server starting", "address", server.Addr); errs <- server.ListenAndServe() }()
	if botToken != "" {
		bot := &telegram.Bot{Client: client, Service: service, Files: files, OCR: engine, WebURL: os.Getenv("WEB_APP_URL")}
		go func() { errs <- bot.Run(ctx) }()
	} else {
		slog.Info("Telegram polling disabled: set TELEGRAM_BOT_TOKEN to enable")
	}
	select {
	case <-ctx.Done():
	case e = <-errs:
		if errors.Is(e, http.ErrServerClosed) {
			e = nil
		}
	}
	cancel()
	stop, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	_ = server.Shutdown(stop)
	return e
}
func main() {
	if e := run(); e != nil {
		slog.Error("server stopped", "error", e)
		os.Exit(1)
	}
}
