package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // bill and metric windows need IANA zones even on minimal images

	"cc-manager/internal/app"
)

// version is injected with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()})))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(version)
			return
		case "hash-password":
			os.Exit(hashPassword())
		case "rotate-key":
			os.Exit(rotateKey(os.Args[2:]))
		case "reset-password":
			os.Exit(resetPassword(os.Args[2:]))
		case "serve":
		default:
			fmt.Fprintf(os.Stderr, "usage: cc-manager [serve|version|hash-password|rotate-key [--check]|reset-password <username>]\n")
			os.Exit(2)
		}
	}
	os.Exit(serve())
}

func logLevel() slog.Level {
	switch strings.ToLower(os.Getenv("CC_MANAGER_LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func hashPassword() int {
	fmt.Fprint(os.Stderr, "Password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "no password provided")
		return 1
	}
	hash, err := app.HashPassword(strings.TrimRight(line, "\r\n"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(hash)
	return 0
}

// rotateKey re-encrypts stored secrets with CC_MANAGER_ENCRYPTION_KEY. Run it
// with the service stopped and the old key(s) listed in
// CC_MANAGER_ENCRYPTION_KEYS_PREVIOUS; --check only reports pending rows.
func rotateKey(args []string) int {
	check := len(args) > 0 && args[0] == "--check"
	cfg, err := app.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	a, err := app.NewWithConfig(cfg, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer a.Close()
	report, err := a.RotateKeys(context.Background(), check)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotation aborted, nothing was changed:", err)
		return 1
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
	return 0
}

// resetPassword sets a user's password from stdin and re-enables the account.
// Run it with the service's environment file; the service may keep running.
func resetPassword(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: cc-manager reset-password <username>")
		return 2
	}
	fmt.Fprint(os.Stderr, "New password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "no password provided")
		return 1
	}
	cfg, err := app.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	a, err := app.NewWithConfig(cfg, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer a.Close()
	if err := a.ResetPassword(args[0], strings.TrimRight(line, "\r\n")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("password updated for", args[0])
	return 0
}

func serve() int {
	cfg, err := app.LoadConfig()
	if err != nil {
		slog.Error("configuration invalid", "error", err)
		return 1
	}
	cfg.Version = version
	addr := os.Getenv("CC_MANAGER_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	a, err := app.NewWithConfig(cfg, true)
	if err != nil {
		slog.Error("startup failed", "error", err)
		return 1
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr, "version", version, "db", cfg.DBPath)
	select {
	case <-ctx.Done():
		slog.Info("shutdown requested")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			_ = a.Close()
			return 1
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http shutdown incomplete", "error", err)
	}
	if err := a.Close(); err != nil {
		slog.Warn("close failed", "error", err)
	}
	slog.Info("stopped")
	return 0
}
