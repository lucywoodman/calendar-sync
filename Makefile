.PHONY: build test clean install

build:
	go build -o calendar-sync ./cmd/calendar-sync

test:
	go test -v ./...

clean:
	rm -f calendar-sync

install:
	go install ./cmd/calendar-sync
