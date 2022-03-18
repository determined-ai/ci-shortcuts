all: build

build:
	go build -o shortcuts .

run: build
	./shortcuts sqlite.db :5729

install: build
	getent passwd shortcuts || useradd --system --shell /bin/true shortcuts
	install shortcuts /usr/local/bin
	mkdir -p /var/cache/shortcuts
	chown shortcuts:shortcuts /var/cache/shortcuts
	cp shortcuts.service /etc/systemd/system
	systemctl daemon-reload
	systemctl enable shortcuts
	systemctl restart shortcuts

clean:
	rm shortcuts
