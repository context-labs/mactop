.PHONY: build run test clean

APP_NAME := mactop

build:
	go build -o $(APP_NAME) main.go

run:
	go run main.go

test:
	go test -v ./...

clean:
	rm -f $(APP_NAME)
