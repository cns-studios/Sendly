COMPOSE_DEV = docker compose
COMPOSE_PROD = docker compose -f docker-compose.yaml -f docker-compose.prod.yaml

.PHONY: dev-build dev-up dev-full prod-build prod-up down logs migrate

dev-build:
	$(COMPOSE_DEV) build

dev-up:
	$(COMPOSE_DEV) up -d

dev-full:
	$(COMPOSE_DEV) up --build -d

prod-build:
	$(COMPOSE_PROD) build

prod-up:
	$(COMPOSE_PROD) up -d

down:
	$(COMPOSE_DEV) down

logs:
	$(COMPOSE_DEV) logs -f app

migrate:
	go run cmd/migrate/main.go