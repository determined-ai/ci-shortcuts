all: build

build:
	go build ./...

run: build
	go run ./... sqlite.db :5729

install: build
	getent passwd shortcuts || useradd --system --shell /bin/true shortcuts
	install shortcuts /usr/local/bin
	mkdir /var/cache/shortcuts
	chown shortcuts:shortcuts /var/cache/shortcuts
	cp shortcuts.service /etc/systemd/system
	systemctl daemon-reload
	systemctl enable shortcuts
	systemctl restart shortcuts

clean:
	rm shortcuts
