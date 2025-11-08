all:
	go build -ldflags="-w -s" -o bin/server
	./bin/server
