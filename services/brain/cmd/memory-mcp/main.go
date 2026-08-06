// memory-mcp — standalone MCP server speaking JSON-RPC 2.0 over stdio.
//
// This is the desktop / CLI entry point: configure it as an MCP
// server in Claude Desktop / Cursor / Continue / any tool that
// follows the MCP spec, and the BiuMind tool palette appears:
// memory.{store,list,recall,delete} plus wiki.{search,list_pages,
// get_page,create_page,update_page,ingest}. Wiki tools that need
// extras (vector search needs an embedder, ingest needs NATS) work
// when those env vars are set; otherwise the tool returns an
// "internal-error" frame at call time and the rest stay usable.
//
// Example claude_desktop_config.json:
//
//	{
//	  "mcpServers": {
//	    "biumind-memory": {
//	      "command": "/usr/local/bin/biu-memory-mcp",
//	      "env": {
//	        "DATABASE_URL":           "postgres://...",
//	        "MEMORY_MCP_USER_ID":     "<your user uuid>",
//	        "MEMORY_MCP_PROJECT_ID": "<your project uuid>"
//	      }
//	    }
//	  }
//	}
//
// Auth note: stdio transport pins the calling user via env, not
// JWT. The desktop client is the trust boundary; if a process can
// spawn the binary it can act as that user. This is fine for local
// single-user scenarios — multi-tenant SaaS uses the HTTP transport
// (services/brain/cmd/brain) where every request is JWT-verified.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	bconfig "github.com/biumind/biumind/packages/go-sdk/biu/config"
	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	bdb "github.com/biumind/biumind/packages/go-sdk/biu/db"
	"github.com/biumind/biumind/packages/go-sdk/biu/embed"
	memorymcp "github.com/biumind/biumind/services/brain/internal/memory/mcp"
	memstore "github.com/biumind/biumind/services/brain/internal/memory/store"
	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	"github.com/biumind/biumind/services/brain/internal/search/vector"
	wikichunks "github.com/biumind/biumind/services/brain/internal/wiki/chunks"
	wikiingest "github.com/biumind/biumind/services/brain/internal/wiki/ingest"
	wikirelevance "github.com/biumind/biumind/services/brain/internal/wiki/relevance"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

type Config struct {
	DatabaseURL string `env:"DATABASE_URL" required:"true"`
	UserID      string `env:"MEMORY_MCP_USER_ID" required:"true"`

	// Optional embedder — same env shape as the brain service so
	// users can copy-paste config. Default "" keeps recall lexical-only
	// AND drops wiki.search to BM25-only.
	EmbedProvider string `env:"EMBED_PROVIDER" default:""`
	EmbedAPIKey   string `env:"EMBED_API_KEY" default:""`
	EmbedBaseURL  string `env:"EMBED_BASE_URL" default:""`
	EmbedModel    string `env:"EMBED_MODEL" default:"text-embedding-3-small"`
	EmbedDims     int    `env:"EMBED_DIMS" default:"1536"`

	// Optional NATS — required only for wiki.ingest. Empty keeps the
	// other 9 tools usable offline; wiki.ingest returns a clear
	// "ingest not configured" error when called without it.
	NatsURL     string `env:"NATS_URL" default:""`
	Environment string `env:"BIUMIND_ENV" default:"dev"`

	// Where stderr logs go. Default "warn" keeps stderr clean so MCP
	// clients don't spam the user with INFO chatter; debug is useful
	// when wiring a new client up.
	LogLevel string `env:"MEMORY_MCP_LOG_LEVEL" default:"warn"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "memory-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg Config
	if err := bconfig.Load(&cfg); err != nil {
		return err
	}
	uid, err := uuid.Parse(cfg.UserID)
	if err != nil {
		return fmt.Errorf("MEMORY_MCP_USER_ID must be a UUID: %w", err)
	}

	level := slog.LevelWarn
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{Level: level})).
		With("component", "memory-mcp")

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := bdb.New(ctx, bdb.Defaults(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	wikiSt := wikistore.New(pool)
	memSrv := memorymcp.New(memstore.New(pool), wikiSt, nil, logger).
		WithSearch(bm25.New(pool), vector.New(wikichunks.New(pool))).
		WithReviews(wikireviews.New(pool)).
		WithRelevance(wikirelevance.New(pool))

	if e, err := buildEmbedder(cfg); err != nil {
		return fmt.Errorf("embedder: %w", err)
	} else if e != nil {
		memSrv = memSrv.WithEmbedder(e)
		logger.Info("embedder configured", "model", e.Model(), "dim", e.Dim())
	}

	// Wiki ingest needs NATS so the worker actually picks the task up.
	// When NATS_URL is unset we still wire the ingest store (so the
	// tool can persist tasks for later replay) but the publisher is
	// the noop bus and the wiki-llm worker won't see them. The MCP
	// server's wiki.ingest tool requires both, so we only wire the
	// pair when NATS connects.
	if cfg.NatsURL != "" {
		nb, err := bus.Connect(cfg.NatsURL, "memory-mcp", cfg.Environment)
		if err != nil {
			return fmt.Errorf("nats: %w", err)
		}
		defer nb.Close()
		pub := publisher.NewBus(nb, cfg.Environment, logger)
		memSrv = memSrv.WithIngest(wikiingest.New(pool), pub)
		logger.Info("nats connected, wiki.ingest enabled",
			"url", cfg.NatsURL, "env", cfg.Environment)
	} else {
		logger.Info("NATS_URL unset — wiki.ingest unavailable in this binary")
	}

	logger.Info("memory-mcp ready", "user_id", uid.String())
	return memSrv.ServeStdio(ctx, os.Stdin, os.Stdout, uid)
}

func buildEmbedder(cfg Config) (embed.Embedder, error) {
	switch cfg.EmbedProvider {
	case "":
		return nil, nil
	case "stub":
		return embed.NewStub(cfg.EmbedDims), nil
	case "openai":
		return embed.NewOpenAI(embed.OpenAIConfig{
			BaseURL: cfg.EmbedBaseURL, APIKey: cfg.EmbedAPIKey,
			Model: cfg.EmbedModel, Dims: cfg.EmbedDims,
		})
	default:
		return nil, fmt.Errorf("unknown EMBED_PROVIDER %q", cfg.EmbedProvider)
	}
}
