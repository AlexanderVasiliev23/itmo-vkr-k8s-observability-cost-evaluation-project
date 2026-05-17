BINARY := obs-bench

.PHONY: build run clean

build:
	go build -o $(BINARY) ./cmd

run: build
	./$(BINARY) run-batch --file experiments-final.yaml

clean:
	rm -f $(BINARY)
