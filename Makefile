-include .env
export

COMPOSE := docker compose -f deploy/compose.dev.yml
TEST_DB := postgres://greyship:greyship@localhost:5434/greyship_test?sslmode=disable
SANDBOX_SOCK := $(HOME)/.lima/greyship/sock/docker.sock
TEST_SANDBOX = $(if $(wildcard $(SANDBOX_SOCK)),GREYSHIP_TEST_SANDBOX_HOST=unix://$(SANDBOX_SOCK))
TEST_S3 := GREYSHIP_TEST_S3_ENDPOINT=http://localhost:3900 GREYSHIP_TEST_S3_REGION=garage \
	GREYSHIP_TEST_S3_ACCESS_KEY=GK677265797368697064657631 \
	GREYSHIP_TEST_S3_SECRET_KEY=6772657973686970206465762073336b6579206e6f7420666f722070726f6421
PKGS = $(shell go list ./... | grep -v /node_modules/)

.PHONY: dev infra sandbox builder scheduler agent router trust-devca up down login hello migrate test test-int lint gen build build-images

infra:
	$(COMPOSE) up -d --wait
	./deploy/garage-init.sh

up: infra sandbox
	./deploy/dev-up.sh

down:
	./deploy/dev-down.sh

login:
	./deploy/dev-login.sh $(EMAIL)

hello:
	@test -f tmp/dev.env || { echo "run make login first"; exit 1; }
	@test -f examples/hello/.env.prod || cp examples/hello/.env.prod.example examples/hello/.env.prod
	. ./tmp/dev.env && cd examples/hello && grey deploy --yes

sandbox:
	@command -v limactl >/dev/null || { echo "limactl missing: brew install lima"; exit 1; }
	limactl list -q | grep -qx greyship || limactl create --name=greyship --tty=false deploy/lima/greyship.yaml
	limactl list greyship --format '{{.Status}}' 2>/dev/null | grep -q Running || limactl start greyship
	./deploy/sandbox-init.sh

builder: migrate
	go run ./cmd/greyship-builder

scheduler: migrate
	go run ./cmd/greyship-scheduler

router: migrate
	go run ./cmd/greyship-router

trust-devca:
	@test -f tmp/devca/ca.pem || { echo "run make router once first; it creates tmp/devca/ca.pem"; exit 1; }
	security add-trusted-cert -r trustRoot -k $(HOME)/Library/Keychains/login.keychain-db tmp/devca/ca.pem

agent: migrate
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o bin/greyship-init ./cmd/greyship-init
	go run ./cmd/greyship-agent

migrate: infra
	go tool goose -dir migrations postgres "$(GREYSHIP_DATABASE_URL)" up

dev: migrate
	go run ./cmd/greyship-api

test:
	go test -race $(PKGS)

test-int: infra
	@$(COMPOSE) exec -T postgres psql -U greyship -tAc "SELECT 1 FROM pg_database WHERE datname='greyship_test'" | grep -q 1 || \
		$(COMPOSE) exec -T postgres createdb -U greyship greyship_test
	GREYSHIP_TEST_DATABASE_URL="$(TEST_DB)" $(TEST_S3) $(TEST_SANDBOX) go test -race -tags integration -count=1 -p 1 $(PKGS)

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint missing: brew install golangci-lint"; exit 1; }
	golangci-lint run

gen:
	go tool sqlc generate

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/greyship-api ./cmd/greyship-api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/greyship-builder ./cmd/greyship-builder
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/greyship-modproxy ./cmd/greyship-modproxy
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/greyship-scheduler ./cmd/greyship-scheduler
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/greyship-agent ./cmd/greyship-agent
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/greyship-router ./cmd/greyship-router
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o bin/greyship-init ./cmd/greyship-init
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/grey ./cmd/greyship
	ln -sf grey bin/gray && ln -sf grey bin/greyship

BUILD_GO_VERSIONS ?= 1.24 1.25 1.26 1.27
FLY_BUILD_APP ?= greyship-build
build-images:
	@mkdir -p tmp/buildimage
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o tmp/buildimage/greyship-buildd ./cmd/greyship-buildd
	cp deploy/fly/build.Dockerfile tmp/buildimage/Dockerfile
	@for v in $(BUILD_GO_VERSIONS); do \
		docker buildx build --platform linux/amd64 --build-arg GO_VERSION=$$v -t registry.fly.io/$(FLY_BUILD_APP):go$$v --push tmp/buildimage || exit 1; \
	done

-include deploy/open-source/release.mk

