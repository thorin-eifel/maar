//go:build openbsd

package main

import "golang.org/x/sys/unix"

// restrict schränkt den Daemon per pledge(2) ein. Gestartete Programme
// (rcctl, vmctl, pfctl …) laufen ohne Einschränkung, da execpromises leer bleibt.
func restrict() error {
	return unix.PledgePromises("stdio rpath wpath cpath dpath tmppath inet fattr chown flock unix dns getpw sendfd recvfd tty proc exec id")
}
