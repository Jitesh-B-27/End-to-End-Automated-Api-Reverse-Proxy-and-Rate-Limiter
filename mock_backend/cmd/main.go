package main

import (
	"log"

	"mock-backend/internal/config"
	"mock-backend/internal/logger"
	"mock-backend/internal/server"
)

// main is intentionally minimal.
// Its only responsibilities are: load config, build logger, start server.
// All routing, middleware, and handler logic lives in the packages they own.
func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log, err := logger.New(cfg.AppEnv)
	if err != nil {
		log.Fatal("failed to initialise logger")
	}
	defer log.Sync() //nolint:errcheck

	srv := server.New(cfg, log)
	if err := srv.Start(); err != nil {
		log.Fatal("server exited with error",
			// zap fields cannot be used here since log is *zap.Logger
		)
	}
}