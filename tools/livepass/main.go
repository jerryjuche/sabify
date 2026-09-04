// Command livepass is a throwaway helper for the manual BMONI sandbox pass
// (doc/bmoni-teacher-kyc.md §10 step 6). It is NOT part of the product:
//
//	go run ./tools/livepass db      — boot an embedded PostgreSQL (port 55432,
//	                                  database sabify_db), apply migrations/*.sql,
//	                                  print READY, then stay alive until killed.
//	go run ./tools/livepass genimg  — write three ≥2KB PNGs into ./tmp/livepass/imgs
//	                                  for the KYC document uploads.
//
// The e2e tests boot the same embedded-postgres; this exists because a live
// pass needs a long-lived server rather than a test process.
//
//	go run ./tools/livepass rows    — dump users + bmoni_wallets rows from the
//	                                  running instance on :55432 (debug aid).
package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	port     = 55432
	database = "sabify_db"
	user     = "postgres"
	password = "postgres"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: livepass db|genimg")
	}
	switch os.Args[1] {
	case "db":
		runDB()
	case "genimg":
		genImages()
	case "rows":
		dumpRows()
	case "pay":
		dumpPayments()
	default:
		log.Fatalf("unknown subcommand %q", os.Args[1])
	}
}

func runDB() {
	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	runtimePath := filepath.Join(root, "tmp", "livepass", "pg")

	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(port).
			Database(database).
			Username(user).
			Password(password).
			RuntimePath(runtimePath),
	)
	if err := pg.Start(); err != nil {
		log.Fatalf("start embedded postgres: %v", err)
	}
	defer func() {
		if err := pg.Stop(); err != nil {
			log.Printf("stop embedded postgres: %v", err)
		}
	}()

	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, password, port, database)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrations(pool)

	log.Printf("LIVEPASS DB READY on %s (db %s) — Ctrl-C to stop", dsn, database)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

// applyMigrations replays migrations/*.sql in dependency order using the
// simple query protocol (multi-statement files + DO $$ blocks included).
func applyMigrations(pool *pgxpool.Pool) {
	ctx := context.Background()
	files := []string{
		"001_initial_schema.sql",
		"002_course_enrollments.sql",
		"002_quiz_retakes.sql",
		"003_bmoni.sql",
		"004_bmoni_owner_key.sql",
		"005_teacher_wallets.sql",
	}
	var combined strings.Builder
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			log.Fatalf("read migration %s: %v", name, err)
		}
		combined.Write(raw)
		combined.WriteString("\n")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Conn().PgConn().Exec(ctx, combined.String()).ReadAll(); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
}

// dumpRows prints the rows a live pass needs to inspect (wallet state,
// user ids) straight from the running Postgres on :55432.
func dumpRows() {
	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, password, port, database)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(context.Background(), `
		SELECT u.email, u.role, w.bmoni_user_id, w.kyc_status, w.kyc_error,
		       COALESCE(w.vba_account_number,''), COALESCE(w.vba_bank_name,''),
		       (w.owner_key_enc IS NOT NULL) AS has_key
		FROM users u LEFT JOIN bmoni_wallets w ON w.user_id = u.id
		ORDER BY u.created_at`)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var email, role, bmoniID, status, errText, vba, bank string
		var hasKey bool
		if err := rows.Scan(&email, &role, &bmoniID, &status, &errText, &vba, &bank, &hasKey); err != nil {
			log.Fatalf("scan: %v", err)
		}
		log.Printf("%-30s %-8s bmoni=%-40s %-18s vba=%s %s key=%v err=%q",
			email, role, bmoniID, status, vba, bank, hasKey, errText)
	}
}

// dumpPayments prints payments joined to their student, course and the
// course teacher's wallet row (BMONI user id) — everything needed to hand-
// deliver a signed employee.deposit.completed webhook during a live pass.
func dumpPayments() {
	dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, password, port, database)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(context.Background(), `
		SELECT p.id, p.status, p.amount_kobo, p.reference,
		       COALESCE(p.paid_at::text,''),
		       su.email, c.title, tu.email,
		       COALESCE(w.bmoni_user_id,''), COALESCE(w.vba_account_number,'')
		FROM payments p
		JOIN users su ON su.id = p.student_id
		JOIN courses c ON c.id = p.course_id
		JOIN users tu ON tu.id = c.teacher_id
		LEFT JOIN bmoni_wallets w ON w.user_id = c.teacher_id
		ORDER BY p.created_at`)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status, ref, paidAt, student, course, teacher, bmoniID, vba string
		var amount int64
		if err := rows.Scan(&id, &status, &amount, &ref, &paidAt, &student, &course, &teacher, &bmoniID, &vba); err != nil {
			log.Fatalf("scan: %v", err)
		}
		log.Printf("%-38s %-8s %6d kobo %-14s paid=%s", id, status, amount, ref, paidAt)
		log.Printf("    student=%-30s course=%-40s teacher=%-30s", student, course, teacher)
		log.Printf("    bmoni_user_id=%s vba=%s", bmoniID, vba)
	}
}

// genImages writes three pseudo-random PNGs (well over 2KB, the size BMONI
// enforces) for the KYC wizard's identification / proof-of-address / selfie
// uploads. doMultipart sniffs real PNG magic bytes, so the files must be
// actual PNGs — not renamed junk.
func genImages() {
	dir := filepath.Join("tmp", "livepass", "imgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20260904))
	for _, name := range []string{"id_document.png", "proof_of_address.png", "selfie.png"} {
		img := image.NewRGBA(image.Rect(0, 0, 400, 300))
		for y := 0; y < 300; y++ {
			for x := 0; x < 400; x++ {
				img.Set(x, y, color.RGBA{
					R: uint8(rng.Intn(256)),
					G: uint8(rng.Intn(256)),
					B: uint8(rng.Intn(256)),
					A: 255,
				})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			log.Fatalf("encode %s: %v", name, err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			log.Fatalf("write %s: %v", name, err)
		}
		log.Printf("wrote %s (%d bytes)", path, buf.Len())
	}
}
