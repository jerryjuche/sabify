include .env

.PHONY: help build run test lint clean db_up db_down

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  build    - Build the application binary"
	@echo "  run      - Run the application"
	@echo "  test     - Run all tests"
	@echo "  lint     - Run go vet"
	@echo "  clean    - Remove build artifacts"
	@echo "  db_up    - Start PostgreSQL via docker-compose"
	@echo "  db_down  - Stop PostgreSQL via docker-compose"
	@echo "  migrate  - Run database migrations"

build:
	go build -o ./tmp/sabify ./cmd/web

run:
	go run ./cmd/web

test:
	go test -v ./...

lint:
	go vet ./...

clean:
	rm -rf ./tmp

db_up:
	docker compose up -d

db_down:
	docker compose down

migrate:
	psql -h localhost -p 5434 -U sabify -d sabify_db -f migrations/001_initial_schema.sql
