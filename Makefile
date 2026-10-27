# maar – Web-Verwaltung für OpenBSD im CTW Design System 2027

VERSION ?= 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := build

.PHONY: all openbsd test check-js demo install clean

all: openbsd

# Cross-Build auf macOS/Linux für OpenBSD (amd64 und arm64).
openbsd:
	CGO_ENABLED=0 GOOS=openbsd GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/maard-openbsd-amd64 ./cmd/maard
	CGO_ENABLED=0 GOOS=openbsd GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/maard-openbsd-arm64 ./cmd/maard

test:
	go vet ./...
	GOOS=openbsd GOARCH=amd64 go vet ./...
	go test -race ./...

# Syntaxprüfung der Oberfläche als ES-Module (braucht node).
check-js:
	@for f in web/js/*.js web/js/views/*.js; do node --input-type=module --check < $$f || { echo "Fehler in $$f"; exit 1; }; done; echo "JS ok"

# Simulierter Host auf http://127.0.0.1:9443 (Login demo / demo).
demo:
	go run ./cmd/maard -demo -webdir web

# Direkt auf dem OpenBSD-Host (pkg_add go) als root ausführen.
install:
	go build -trimpath -ldflags "$(LDFLAGS)" -o /usr/local/sbin/maard ./cmd/maard
	install -m 0555 etc/rc.d/maard /etc/rc.d/maard
	install -d -m 0700 /var/maar/backups

clean:
	rm -rf $(BIN)
