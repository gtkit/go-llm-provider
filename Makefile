.PHONY: tool check tag release-check-root release-check-v2 release-check


LINT_TARGETS ?= ./...
MIN_PROVIDER_COVERAGE ?= 80.0

tool: ## Lint Go code with the installed golangci-lint
	@ echo "▶️ golangci-lint run"
	golangci-lint run $(LINT_TARGETS)
	gofumpt -l -w .
	@ echo "✅ golangci-lint run"

## govulncheck 检查漏洞 go install golang.org/x/vuln/cmd/govulncheck@latest
check:
	govulncheck $(LINT_TARGETS)

release-check-root: ## Run release checks for root module and enforce provider coverage threshold
	go vet ./...
	golangci-lint run ./...
	go test -race -count=1 -timeout=5m ./...
	go test -bench=. -benchmem -count=3 ./...
	go test -coverprofile=coverage.out ./provider
	@cover=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	awk -v got="$$cover" -v min="$(MIN_PROVIDER_COVERAGE)" 'BEGIN { if (got+0 < min+0) exit 1 }' || \
		{ echo "provider coverage $$cover% is below $(MIN_PROVIDER_COVERAGE)%"; exit 1; }; \
	echo "provider coverage $$cover% (threshold $(MIN_PROVIDER_COVERAGE)%)"

release-check-v2: ## Run release checks for v2 module and enforce provider coverage threshold
	cd v2 && go vet ./...
	cd v2 && golangci-lint run ./...
	cd v2 && go test -race -count=1 -timeout=5m ./...
	cd v2 && go test -bench=. -benchmem -count=3 ./...
	cd v2 && go test -coverprofile=coverage.out ./provider
	@cover=$$(cd v2 && go tool cover -func=coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	awk -v got="$$cover" -v min="$(MIN_PROVIDER_COVERAGE)" 'BEGIN { if (got+0 < min+0) exit 1 }' || \
		{ echo "v2 provider coverage $$cover% is below $(MIN_PROVIDER_COVERAGE)%"; exit 1; }; \
	echo "v2 provider coverage $$cover% (threshold $(MIN_PROVIDER_COVERAGE)%)"

release-check: release-check-root release-check-v2 ## Run release checks for both root and v2 modules

## 推送标签到远程仓库时，通常不需要指定分支
tag:
	@current=$$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' version.go | head -n1 | tr -d 'v'); \
	if [ -z "$$current" ]; then echo "version not found in version.go"; exit 1; fi; \
	maj=$$(echo $$current | cut -d. -f1); \
	min=$$(echo $$current | cut -d. -f2); \
	patch=$$(echo $$current | cut -d. -f3); \
	newpatch=$$(expr $$patch + 1); \
	new="v$$maj.$$min.$$newpatch"; \
	printf "Bump: v%s -> %s\n" "$$current" "$$new"; \
	sed -E -i.bak 's/(const Version = ")([^"]+)(")/\1'"$$new"'\3/' version.go; \
	git add version.go; \
	git commit -m "chore(release): $$new"; \
	printf "Release: %s\n" "$$new"; \
	git push gtkit HEAD; \
	git tag -a "$$new" -m "release $$new"; \
	printf "Tag: %s\n" "$$new"; \
	git push gtkit "$$new"; \
	printf "Done\n"
	rm -f version.go.bak
