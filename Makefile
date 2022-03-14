all: run

build:
	go build ./...

run: build
	go run ./... sqlite.db :1323

install:
	cp shortcuts.service /etc/systemd/system
	systemctl daemon-reload
	systemctl enable --now shortcuts

clean:
	rm shortcuts
