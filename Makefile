# The repository is several Go modules, so "go test ./..." at the root only
# covers the core one. These targets fan out across all of them.
MODULES := . adapter/gin adapter/fiber adapter/echo example

.PHONY: test
test:
	@for module in $(MODULES); do \
		echo "== $$module =="; \
		(cd $$module && go test -race -count=1 ./...) || exit 1; \
	done

.PHONY: vet
vet:
	@for module in $(MODULES); do \
		(cd $$module && go vet ./...) || exit 1; \
	done

.PHONY: tidy
tidy:
	@for module in $(MODULES); do \
		(cd $$module && GOWORK=off go mod tidy) || exit 1; \
	done

.PHONY: fmt
fmt:
	gofmt -w .

# golangci-lint covers govet and gofmt among its linters, so this supersedes
# the vet target rather than sitting beside it. Install it with:
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
.PHONY: lint
lint:
	@for module in $(MODULES); do \
		echo "== $$module =="; \
		(cd $$module && golangci-lint run --config $(CURDIR)/.golangci.yml ./...) || exit 1; \
	done

.PHONY: build
build:
	go build -o bin/vitekit ./cmd/vitekit

.PHONY: example
example:
	npm --prefix example/frontend install
	go run ./cmd/vitekit dev --dir ./example/frontend --backend "go run ./gin" --backend-dir ./example
