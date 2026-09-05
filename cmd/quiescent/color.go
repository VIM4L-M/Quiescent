package main

import "os"

var colorEnabled = os.Getenv("NO_COLOR") == ""

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiTeal  = "\x1b[38;2;94;234;212m"
	ansiAmber = "\x1b[38;2;242;177;52m"
	ansiMuted = "\x1b[38;2;125;138;156m"
)

func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func green(s string) string { return paint(ansiGreen, s) }
func red(s string) string   { return paint(ansiRed, s) }
func bold(s string) string  { return paint(ansiBold, s) }
func teal(s string) string  { return paint(ansiTeal, s) }
func amber(s string) string { return paint(ansiAmber, s) }
func muted(s string) string { return paint(ansiMuted, s) }
