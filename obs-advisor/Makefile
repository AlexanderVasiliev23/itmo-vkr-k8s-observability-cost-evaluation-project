DB ?= ../data/obs_bench_results.db
PORT ?= 8086

.PHONY: run build

build:
	cd server && go build -o obs-advisor .

run: build
	./server/obs-advisor --db $(DB) --port $(PORT) --static $(CURDIR)
