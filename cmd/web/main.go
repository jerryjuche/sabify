package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"sabify/internal/models"
)

type application struct {
	config        config
	logger        *slog.Logger
	models        models.Models
	templateCache map[string]*template.Template
	session       *scs.SessionManager
}

type config struct {
	addr string
	db   struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  time.Duration
	}
}

func main() {
	var cfg config

	flag.StringVar(&cfg.addr, "addr", ":4000", "HTTP network address")
	flag.StringVar(&cfg.db.dsn, "db-dsn", "", "PostgreSQL DSN")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "Max open DB connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "Max idle DB connections")
	flag.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", 5*time.Minute, "Max DB idle time")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	if err := godotenv.Load(); err != nil {
		logger.Error("failed to load .env file", "error", err)
		os.Exit(1)
	}

	if cfg.db.dsn == "" {
		cfg.db.dsn = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=%s",
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
			os.Getenv("DB_SSLMODE"),
		)
	}

	if os.Getenv("APP_PORT") != "" {
		cfg.addr = ":" + os.Getenv("APP_PORT")
	}

	templateCache, err := newTemplateCache()
	if err != nil {
		logger.Error("failed to create template cache", "error", err)
		os.Exit(1)
	}

	dbPool, err := openDB(cfg)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	logger.Info("database connection pool established")

	session := scs.New()
	session.Lifetime = 24 * time.Hour
	session.Cookie.Secure = true
	session.Cookie.SameSite = http.SameSiteLaxMode

	app := &application{
		config:        cfg,
		logger:        logger,
		models:        models.NewModels(dbPool),
		templateCache: templateCache,
		session:       session,
	}

	srv := &http.Server{
		Addr:         cfg.addr,
		Handler:      app.routes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server starting", "addr", cfg.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	logger.Info("server shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("forced shutdown", "error", err)
	}

	logger.Info("server stopped")
}

func openDB(cfg config) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.db.dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to create connection pool: %w", err)
	}

	poolConfig := pool.Config()
	poolConfig.MaxConns = int32(cfg.db.maxOpenConns)
	poolConfig.MinConns = int32(cfg.db.maxIdleConns)
	poolConfig.MaxConnLifetime = cfg.db.maxIdleTime
	poolConfig.MaxConnIdleTime = cfg.db.maxIdleTime

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	return pool, nil
}

/*
 * Template helpers available inside every template.
 */

var templateFuncs = template.FuncMap{
	"add": func(a, b int) int {
		return a + b
	},
	"multiply": func(a, b int) int {
		return a * b
	},
	"initials": func(name string) string {
		parts := strings.Fields(strings.TrimSpace(name))
		if len(parts) == 0 {
			return "?"
		}
		first := strings.ToUpper(parts[0][:1])
		if len(parts) > 1 {
			return first + strings.ToUpper(parts[len(parts)-1][:1])
		}
		return first
	},
	"shortDate": func(t time.Time) string {
		return t.Format("Jan 2")
	},
}

func newTemplateCache() (map[string]*template.Template, error) {
	cache := map[string]*template.Template{}

	pages, err := filepath.Glob("./ui/html/pages/*/*.html")
	if err != nil {
		return nil, err
	}

	authFiles, err := filepath.Glob("./ui/html/auth/*.html")
	if err != nil {
		return nil, err
	}

	studentFiles, err := filepath.Glob("./ui/html/student/*.html")
	if err != nil {
		return nil, err
	}

	teacherFiles, err := filepath.Glob("./ui/html/teacher/*.html")
	if err != nil {
		return nil, err
	}

	files := append(pages, authFiles...)
	files = append(files, studentFiles...)
	files = append(files, teacherFiles...)

	for _, file := range files {
		rel, err := filepath.Rel("./ui/html", file)

		/*
		 * Key templates by their path relative to
		 * ui/html (e.g. "student/courses.html") so
		 * identically-named pages in different role
		 * folders can never collide.
		 */
		if err != nil {
			return nil, err
		}
		name := filepath.ToSlash(rel)

		ts, err := template.New("base").Funcs(templateFuncs).ParseFiles("./ui/html/layouts/base.html")
		if err != nil {
			return nil, err
		}

		ts, err = ts.ParseGlob("./ui/html/components/*.html")
		if err != nil {
			return nil, err
		}

		ts, err = ts.ParseGlob("./ui/html/auth/*.html")
		if err != nil {
			return nil, err
		}

		ts, err = ts.ParseFiles(file)
		if err != nil {
			return nil, err
		}

		cache[name] = ts
	}

	return cache, nil
}
