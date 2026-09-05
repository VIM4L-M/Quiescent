package main

import "fmt"

const bannerArt = ` █▀█ █ █ █ █▀▀ █▀▀ █▀▀ █▀▀ █▄ █ ▀█▀
 ▀▀█ █▄█ █ ██▄ ▄▄█ █▄▄ ██▄ █ ▀█  █ `

func printBanner() {
	fmt.Println(amber(bold(bannerArt)))
	fmt.Println(muted(" a durable retry sequencer for failed mandate debits"))
	fmt.Println()
}
