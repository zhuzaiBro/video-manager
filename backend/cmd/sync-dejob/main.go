package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/zood/video-manager/internal/dejobsync"
)

func main() {
	token, dsn := os.Getenv("DEJOB_USER_TOKEN"), os.Getenv("DEJOB_DATABASE_URL")
	if token == "" || dsn == "" {
		log.Fatal("DEJOB_USER_TOKEN and DEJOB_DATABASE_URL are required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal("invalid DeJob database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err = dejobsync.Run(ctx, db, token); err != nil {
		log.Fatal(err)
	}
}
