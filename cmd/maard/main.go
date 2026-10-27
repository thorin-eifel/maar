// maar – Web-Verwaltung für OpenBSD im CTW Design System 2027.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"log/syslog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thorin-eifel/maar/internal/auth"
	"github.com/thorin-eifel/maar/internal/console"
	"github.com/thorin-eifel/maar/internal/jobs"
	"github.com/thorin-eifel/maar/internal/metrics"
	"github.com/thorin-eifel/maar/internal/obsd"
	"github.com/thorin-eifel/maar/internal/sched"
	"github.com/thorin-eifel/maar/internal/server"
	"github.com/thorin-eifel/maar/web"
)

var version = "1.0.0"

func main() {
	listen := flag.String("listen", ":9443", "Adresse und Port")
	cert := flag.String("cert", "/etc/ssl/maar.crt", "TLS-Zertifikat (wird bei Bedarf selbst signiert erzeugt)")
	key := flag.String("key", "/etc/ssl/private/maar.key", "TLS-Schlüssel")
	plain := flag.Bool("plain-http", false, "ohne TLS lauschen (nur hinter relayd/httpd auf localhost)")
	group := flag.String("group", "wheel", "Gruppe, deren Mitglieder sich anmelden dürfen")
	vmdir := flag.String("vmdir", "/var/vmd", "Verzeichnis für Disk-Images und ISOs")
	demo := flag.Bool("demo", false, "Demo-Modus mit simuliertem OpenBSD-Host (Login demo/demo)")
	noPledge := flag.Bool("no-pledge", false, "pledge(2) nicht aktivieren")
	webdir := flag.String("webdir", "", "Oberfläche aus diesem Verzeichnis statt eingebettet ausliefern (Entwicklung)")
	showVersion := flag.Bool("version", false, "Version ausgeben")
	flag.Parse()

	if *showVersion {
		fmt.Println("maar", version)
		return
	}
	if *demo {
		*plain = true
		if *listen == ":9443" {
			*listen = "127.0.0.1:9443"
		}
	}

	var (
		runner   obsd.Runner   = obsd.ExecRunner{Timeout: 2 * time.Minute}
		root                   = "/"
		verifier auth.Verifier = auth.BSDAuth{}
		audit    *log.Logger
	)
	if *demo {
		d, r, err := obsd.NewDemo()
		if err != nil {
			log.Fatal(err)
		}
		runner, root, verifier = d, r, auth.DemoAuth{User: "demo", Pass: "demo"}
		audit = log.New(os.Stderr, "audit: ", log.LstdFlags)
		log.Printf("Demo-Modus: Sandbox unter %s, Anmeldung mit demo / demo", r)
		defer os.RemoveAll(r)
	} else {
		if os.Geteuid() != 0 {
			log.Fatal("maar muss als root laufen (Systemverwaltung und BSD Auth). Für Tests: -demo")
		}
		if w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "maar"); err == nil {
			audit = log.New(w, "", 0)
		} else {
			audit = log.New(os.Stderr, "audit: ", log.LstdFlags)
		}
	}

	host := obsd.NewHost(runner, root, *demo)
	host.VMDir = filepath.Clean(*vmdir)
	if *demo {
		// Demo-Konto wird wie ein wheel-Mitglied behandelt.
		*group = ""
	}

	var static fs.FS
	if *webdir != "" {
		static = os.DirFS(*webdir)
	} else {
		static = web.FS
	}

	jm := jobs.NewManager(runner)
	scheduler := sched.New(host, jm, audit)
	scheduler.Start(context.Background())
	collector := metrics.NewCollector(host, audit)
	go collector.Run(context.Background())
	srv := &server.Server{
		Host: host, Auth: verifier, Sessions: auth.NewStore(), Jobs: jm, Sched: scheduler, Metrics: collector,
		Hub: console.NewHub(), Static: static, AdminGroup: *group, Secure: !*plain, Audit: audit, Version: version,
	}

	if *demo && !isLoopback(*listen) {
		log.Fatal("Der Demo-Modus öffnet eine lokale Shell und darf nur auf localhost lauschen (z. B. -listen 127.0.0.1:9443)")
	}
	if *plain && !isLoopback(*listen) && !*demo {
		log.Printf("WARNUNG: -plain-http auf %s – Passwörter und Sitzungen gehen unverschlüsselt über das Netz", *listen)
	}

	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(os.Stderr, "http: ", log.LstdFlags),
	}

	if !*plain {
		if err := ensureCert(*cert, *key); err != nil {
			log.Fatalf("TLS-Zertifikat: %v", err)
		}
		hs.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	if !*noPledge && !*demo {
		if err := restrict(); err != nil {
			log.Fatalf("pledge: %v", err)
		}
	}
	scheme := "https"
	if *plain {
		scheme = "http"
	}
	log.Printf("maar %s lauscht auf %s://%s", version, scheme, ln.Addr())
	if *plain {
		err = hs.Serve(ln)
	} else {
		err = hs.ServeTLS(ln, *cert, *key)
	}
	log.Fatal(err)
}

func isLoopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ensureCert erzeugt bei Bedarf ein selbst signiertes ECDSA-Zertifikat.
func ensureCert(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); err == nil {
		return nil
	}
	hn, _ := os.Hostname()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hn, Organization: []string{"maar"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(3, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{hn, strings.SplitN(hn, ".", 2)[0], "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(keyPath), 0o700)
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	log.Printf("Selbst signiertes Zertifikat erzeugt: %s (SHA-256 im Browser prüfen)", certPath)
	return nil
}
