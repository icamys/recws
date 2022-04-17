install:
	go get

linter:
	golangci-lint run

ls-lint:
	curl -sL -o ls-lint https://github.com/loeffel-io/ls-lint/releases/download/v1.11.0/ls-lint-linux && chmod +x ls-lint && ./ls-lint

test:
	make linter