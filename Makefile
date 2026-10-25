.PHONY: build test test-ui test-browser test-workspace check demo clean
NODE ?= node

build:
	go build -o bin/gometer ./cmd/gometer
	go build -o bin/gometer-demo ./cmd/gometer-demo

test: test-ui
	go test -race ./...

test-ui:
	$(NODE) --check internal/team/assets/workspace.js
	$(NODE) --check internal/dashboard/assets/theme.js
	$(NODE) --check internal/dashboard/assets/navigation.js
	$(NODE) --check internal/dashboard/assets/workers.js
	$(NODE) --check internal/dashboard/assets/tests.js
	$(NODE) --check internal/dashboard/assets/app.js
	$(NODE) --check internal/dashboard/assets/results.js
	$(NODE) --check internal/dashboard/assets/jmx.js
	$(NODE) --check internal/dashboard/assets/csv.js
	$(NODE) --check internal/dashboard/assets/shortcuts.js
	$(NODE) --check internal/dashboard/assets/journey-editor.js
	$(NODE) --test internal/dashboard/test/*.test.cjs

test-browser: build
	$(NODE) internal/dashboard/test/journey-browser.cjs

test-workspace:
	$(NODE) scripts/test-workspace-postgres.cjs

check: test-ui
	go vet ./...
	go test -race ./...

demo:
	go run ./cmd/gometer-demo

clean:
	rm -rf bin
