include .env

.PHONY: help build run test lint clean db_up db_down migrate bootstrap setup

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
	@echo "  bootstrap- Provision the platform BMONI wallet (needs BMONI_API_KEY)"
	@echo "  setup    - db_up + migrate + bootstrap (one-shot dev bootstrap)"

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
	sudo docker compose up -d

db_down:
	sudo docker compose down

migrate:
	@sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/001_initial_schema.sql
	@sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/002_course_enrollments.sql
	@sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/002_quiz_retakes.sql
	@sudo docker exec -i sabify-postgres psql -U sabify -d sabify_db < migrations/003_bmoni.sql

bootstrap:
	@if [ -z "$(BMONI_API_KEY)" ]; then \
		echo "BMONI_API_KEY is not set in .env — skipping wallet provisioning."; \
		echo "Run 'BMONI_API_KEY=... make bootstrap' to provision the platform wallet."; \
	else \
		BMONI_API_KEY=$(BMONI_API_KEY) go run ./tools/bmoni-bootstrap; \
	fi

setup: db_up migrate bootstrap
	@echo ""
	@echo "DB up, migrations applied, wallet provisioned."
	@echo "Start the app with: make run"
