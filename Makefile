AWS_PROFILE ?= webkwizards
export AWS_PROFILE

GOFLAGS := -trimpath -ldflags="-s -w" -tags lambda.norpc
BUILD   := build

.PHONY: build test deploy plan destroy logs fmt clean

build:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) -o $(BUILD)/webhook/bootstrap ./cmd/webhook
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(GOFLAGS) -o $(BUILD)/worker/bootstrap ./cmd/worker

test:
	go vet ./...
	go test ./...

plan: build
	terraform -chdir=infra init -upgrade=false
	terraform -chdir=infra plan

deploy: test build
	terraform -chdir=infra init -upgrade=false
	terraform -chdir=infra apply

destroy:
	terraform -chdir=infra destroy

# Tail logs: make logs FN=worker  (or FN=webhook)
FN ?= worker
logs:
	aws logs tail /aws/lambda/tg-summary-$(FN) --follow --format short

fmt:
	gofmt -w .
	terraform -chdir=infra fmt

clean:
	rm -rf $(BUILD)
