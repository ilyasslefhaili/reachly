.PHONY: run test build install uninstall fmt

# Where `go install` puts binaries (GOBIN if set, else GOPATH/bin).
BIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

run:
	go run ./cmd/reachly serve

test:
	go test -race ./...

build:
	go build -o bin/reachly ./cmd/reachly

# Installs `reachly` so it runs from any directory. Adds the Go bin folder to
# your PATH in your shell config if it is not there yet (only once).
install:
	go install ./cmd/reachly
	@case "$$SHELL" in *zsh) RC="$$HOME/.zshrc" ;; *bash) RC="$$HOME/.bash_profile" ;; *) RC="$$HOME/.profile" ;; esac; \
	case ":$$PATH:" in \
	  *":$(BIN_DIR):"*) echo "✔ installed $(BIN_DIR)/reachly. Run: reachly --help" ;; \
	  *) if ! grep -qs 'reachly: go bin on PATH' "$$RC"; then \
	       printf '\n# reachly: go bin on PATH\nexport PATH="$$PATH:$(BIN_DIR)"\n' >> "$$RC"; \
	       echo "✔ added $(BIN_DIR) to PATH in $$RC"; \
	     fi; \
	     echo "✔ installed $(BIN_DIR)/reachly"; \
	     echo "→ open a new terminal (or run: source $$RC), then: reachly --help" ;; \
	esac

uninstall:
	rm -f "$(BIN_DIR)/reachly"
	@echo "✔ removed $(BIN_DIR)/reachly (the '# reachly: go bin on PATH' line in your shell config is harmless; delete it if you like)"

fmt:
	gofmt -w .
